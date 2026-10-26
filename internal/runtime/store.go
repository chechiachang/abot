package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gofrs/flock"
	"google.golang.org/adk/session"
	adksqlite "google.golang.org/adk/session/database"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	db         *gorm.DB
	sessions   session.Service
	path       string
	readOnly   bool
	lock       *flock.Flock
	mutationMu sync.Mutex
}

var ErrRuntimeInUse = errors.New("runtime database is already open by another process")

func Open(path string) (*Store, error) {
	return open(path, false, true, true, true)
}

func OpenReadOnly(path string) (*Store, error) {
	return open(path, true, false, false, false)
}

// OpenAdmin opens the existing SQLite database for brief metadata edits while
// the bot owns the main writer lock. It does not migrate or recover runtime state.
func OpenAdmin(path string) (*Store, error) {
	return open(path, false, false, false, false)
}

func open(path string, readOnly, exclusive, migrate, recover bool) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("runtime SQLite path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime SQLite path: %w", err)
	}
	if readOnly || !migrate {
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("inspect runtime database: %w", err)
		}
	} else if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, fmt.Errorf("create runtime state directory: %w", err)
	}
	var lock *flock.Flock
	opened := false
	defer func() {
		if !opened && lock != nil {
			_ = lock.Unlock()
		}
	}()
	if !readOnly && exclusive {
		lock = flock.New(abs+".lock", flock.SetPermissions(0600))
		locked, err := lock.TryLock()
		if err != nil {
			return nil, fmt.Errorf("lock runtime database: %w", err)
		}
		if !locked {
			return nil, ErrRuntimeInUse

		}
	}
	dsn := sqliteDSN(abs, readOnly)
	gormConfig := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	db, err := gorm.Open(sqlite.Open(dsn), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("open runtime metadata database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("open runtime metadata connection: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if !readOnly && migrate {
		if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("enable SQLite WAL: %w", err)
		}
		for _, index := range []string{"idx_runtime_telegram_outbox_telegram_message_id", "idx_runtime_outbox_chat_message"} {
			if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
				sqlDB.Close()
				return nil, fmt.Errorf("migrate Telegram outbox message index: %w", err)
			}
		}
		if err := db.AutoMigrate(&Branch{}, &BranchRevision{}, &Task{}, &TaskRecord{}, &ProgressStep{}, &ImportRun{}, &QueuedInput{}, &Attachment{}, &TelegramUpdate{}, &RuntimeValue{}, &PassiveMessage{}, &Outbox{}, &AccessRequest{}, &Conversation{}); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("migrate runtime metadata schema: %w", err)
		}
		if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_active_turn ON runtime_tasks(chat_id, branch_id) WHERE status IN ('pending', 'running')`).Error; err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("migrate active-turn index: %w", err)
		}
	}
	sessions, err := adksqlite.NewSessionService(sqlite.Open(dsn), gormConfig)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("open ADK SQLite session service: %w", err)
	}
	if !readOnly && migrate {
		if err := adksqlite.AutoMigrate(sessions); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("migrate ADK session schema: %w", err)
		}
	}
	if !readOnly && migrate {
		if err := os.Chmod(abs, 0600); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("restrict runtime database permissions: %w", err)
		}
	}
	store := &Store{db: db, sessions: sessions, path: abs, readOnly: readOnly, lock: lock}
	if recover {
		if err := store.RecoverUnfinished(context.Background()); err != nil {
			store.Close()
			return nil, err
		}
		if err := store.RecoverTelegramDelivery(context.Background()); err != nil {
			store.Close()
			return nil, err
		}
	}
	opened = true
	return store, nil
}

func sqliteDSN(path string, readOnly bool) string {
	query := url.Values{}
	if readOnly {
		query.Set("mode", "ro")
	} else {
		query.Set("mode", "rwc")
	}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	return "file:" + path + "?" + query.Encode()
}

func (s *Store) requireWrite() error {
	if s.readOnly {
		return errors.New("runtime database is read-only")
	}
	return nil
}

func (s *Store) Sessions() session.Service { return s.sessions }

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	dbErr := db.Close()
	var lockErr error
	if s.lock != nil {
		lockErr = s.lock.Unlock()
	}
	return errors.Join(dbErr, lockErr)
}

func (s *Store) BeginTurn(ctx context.Context, chatID, branchID, generationID string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.requireWrite(); err != nil {
		return err
	}
	if chatID == "" || branchID == "" || generationID == "" {
		return errors.New("chat, branch, and generation IDs are required")
	}
	branch := Branch{ChatID: chatID, BranchID: branchID, SessionID: defaultSessionID(chatID, branchID), CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Where(Branch{ChatID: chatID, BranchID: branchID}).FirstOrCreate(&branch).Error; err != nil {
		return fmt.Errorf("load runtime branch: %w", err)
	}
	task := Task{ID: generationID, ChatID: chatID, BranchID: branchID, Status: TaskRunning, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(&task).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return ErrActiveTurn
		}
		return fmt.Errorf("start agent generation: %w", err)
	}
	return nil
}

func (s *Store) FinishTurn(ctx context.Context, generationID string, status TaskStatus, eventID, errorKind string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if status != TaskCompleted && status != TaskFailed && status != TaskCancelled && status != TaskBlocked {
		return fmt.Errorf("invalid terminal task status %q", status)
	}
	updates := map[string]any{"status": status, "updated_at": time.Now().UTC(), "finished_at": time.Now().UTC(), "result_event_id": eventID, "error_kind": errorKind}
	result := s.db.WithContext(ctx).Model(&Task{}).Where("id = ? AND status IN ?", generationID, []TaskStatus{TaskPending, TaskRunning}).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("finish agent generation: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("active generation %q not found", generationID)
	}
	return nil
}

func (s *Store) RecoverUnfinished(ctx context.Context) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Task{}).Where("status IN ?", []TaskStatus{TaskPending, TaskRunning}).Updates(map[string]any{
			"status": TaskUncertain, "updated_at": now, "finished_at": now, "error_kind": "process_interrupted",
		}).Error; err != nil {
			return fmt.Errorf("mark interrupted generations uncertain: %w", err)
		}
		if err := tx.Model(&TaskRecord{}).Where("status IN ?", []RecordStatus{RecordPending, RecordRunning}).Updates(map[string]any{
			"status": RecordUncertain, "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("mark interrupted tool tasks uncertain: %w", err)
		}
		if err := tx.Model(&QueuedInput{}).Where("status = ?", InputClaimed).Updates(map[string]any{
			"status": InputQueued, "claimed_at": nil,
		}).Error; err != nil {
			return fmt.Errorf("requeue interrupted inputs: %w", err)
		}
		return nil
	})
}
