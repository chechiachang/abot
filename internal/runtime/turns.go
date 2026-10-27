package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ActiveTurn struct {
	ID     string
	cancel context.CancelFunc
}

type TurnManager struct {
	mu     sync.Mutex
	active map[string]ActiveTurn
}

func NewTurnManager() *TurnManager {
	return &TurnManager{active: make(map[string]ActiveTurn)}
}

func (m *TurnManager) Start(parent context.Context, chatID, branchID string) (context.Context, func(), error) {
	if m == nil || chatID == "" || branchID == "" {
		return nil, nil, errors.New("turn manager, chat, and branch are required")
	}
	ctx, cancel := context.WithCancel(parent)
	key, id := turnKey(chatID, branchID), uuid.NewString()
	m.mu.Lock()
	if _, exists := m.active[key]; exists {
		m.mu.Unlock()
		cancel()
		return nil, nil, ErrActiveTurn
	}
	m.active[key] = ActiveTurn{ID: id, cancel: cancel}
	m.mu.Unlock()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			m.mu.Lock()
			if current, exists := m.active[key]; exists && current.ID == id {
				delete(m.active, key)
			}
			m.mu.Unlock()
			cancel()
		})
	}
	return ctx, finish, nil
}

func (m *TurnManager) ActiveRun(chatID, branchID string) (ActiveTurn, error) {
	if m == nil {
		return ActiveTurn{}, ErrActiveTurn
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.active[turnKey(chatID, branchID)]
	if !ok {
		return ActiveTurn{}, ErrActiveTurn
	}
	return ActiveTurn{ID: current.ID}, nil
}

func (m *TurnManager) CancelRun(chatID, branchID, runID string) bool {
	if m == nil || chatID == "" || branchID == "" || runID == "" {
		return false
	}
	m.mu.Lock()
	current, ok := m.active[turnKey(chatID, branchID)]
	if ok && current.ID == runID {
		current.cancel()
	} else {
		ok = false
	}
	m.mu.Unlock()
	return ok
}

func (m *TurnManager) Cancel(chatID, branchID string) bool {
	if m == nil || chatID == "" || branchID == "" {
		return false
	}
	m.mu.Lock()
	current, exists := m.active[turnKey(chatID, branchID)]
	m.mu.Unlock()
	if exists {
		current.cancel()
	}
	return exists
}

func (m *TurnManager) Active(chatID, branchID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.active[turnKey(chatID, branchID)]
	return exists
}

func turnKey(chatID, branchID string) string { return chatID + "\x00" + branchID }

func (s *Store) EnqueueInput(ctx context.Context, chatID, branchID string, kind InputKind, text string) (QueuedInput, error) {
	if err := s.requireWrite(); err != nil {
		return QueuedInput{}, err
	}
	text = strings.TrimSpace(text)
	if chatID == "" || branchID == "" || text == "" || len(text) > 64<<10 {
		return QueuedInput{}, errors.New("chat, branch, and input text of 1 to 65536 bytes are required")
	}
	if kind != InputSteering && kind != InputFollowup {
		return QueuedInput{}, fmt.Errorf("unsupported queued input kind %q", kind)
	}
	input := QueuedInput{ID: uuid.NewString(), ChatID: chatID, BranchID: branchID, Kind: kind, Status: InputQueued, Text: text, CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(&input).Error; err != nil {
		return QueuedInput{}, fmt.Errorf("queue turn input: %w", err)
	}
	return input, nil
}

func (s *Store) EnqueueTelegramTurn(ctx context.Context, chatID, branchID, userID string, senderID, updateID, replyTo int64, kind InputKind, text string) (QueuedInput, error) {
	if err := s.requireWrite(); err != nil {
		return QueuedInput{}, err
	}
	text = strings.TrimSpace(text)
	if chatID == "" || branchID == "" || userID == "" || senderID <= 0 || updateID <= 0 || text == "" || len(text) > 64<<10 {
		return QueuedInput{}, errors.New("chat, branch, user, sender, update, and input text of 1 to 65536 bytes are required")
	}
	if kind != InputSteering && kind != InputFollowup && kind != InputArticle {
		return QueuedInput{}, fmt.Errorf("unsupported queued input kind %q", kind)
	}
	input := QueuedInput{ID: uuid.NewString(), ChatID: chatID, BranchID: branchID, UserID: userID, SenderID: senderID, UpdateID: updateID, ReplyToMessageID: replyTo, Kind: kind, Status: InputQueued, Text: text, CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(&input).Error; err != nil {
		return QueuedInput{}, fmt.Errorf("queue Telegram turn: %w", err)
	}
	return input, nil
}

func (s *Store) ClaimInputs(ctx context.Context, chatID, branchID string, limit int) ([]QueuedInput, error) {
	if err := s.requireWrite(); err != nil {
		return nil, err
	}
	if chatID == "" || branchID == "" {
		return nil, errors.New("chat and branch IDs are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	var claimed []QueuedInput
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var queued []QueuedInput
		if err := tx.Where("chat_id = ? AND branch_id = ? AND status = ?", chatID, branchID, InputQueued).
			Order("CASE kind WHEN 'steering' THEN 0 ELSE 1 END, created_at, id").Limit(limit).Find(&queued).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, input := range queued {
			result := tx.Model(&QueuedInput{}).Where("id = ? AND status = ?", input.ID, InputQueued).Updates(map[string]any{"status": InputClaimed, "claimed_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				input.Status, input.ClaimedAt = InputClaimed, &now
				claimed = append(claimed, input)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim queued inputs: %w", err)
	}
	return claimed, nil
}

func (s *Store) CompleteInput(ctx context.Context, inputID string, failureKind string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	status := InputCompleted
	if failureKind != "" {
		status = InputFailed
		if len(failureKind) > 64 {
			return errors.New("input failure kind exceeds 64 characters")
		}
		for _, char := range failureKind {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
				return errors.New("input failure kind must be a safe identifier")
			}
		}
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&QueuedInput{}).Where("id = ? AND status = ?", inputID, InputClaimed).Updates(map[string]any{"status": status, "finished_at": now, "error_kind": failureKind})
	if result.Error != nil {
		return fmt.Errorf("complete queued input: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("claimed input %q not found", inputID)
	}
	return nil
}

func (s *Store) ReleaseInput(ctx context.Context, inputID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&QueuedInput{}).Where("id = ? AND status = ?", inputID, InputClaimed).Updates(map[string]any{"status": InputQueued, "claimed_at": nil})
	if result.Error != nil {
		return fmt.Errorf("release queued input: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("claimed input %q not found", inputID)
	}
	return nil
}

func (s *Store) ListInputs(ctx context.Context, chatID string, limit int) ([]QueuedInput, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := s.db.WithContext(ctx).Order("created_at desc").Limit(limit)
	if chatID != "" {
		query = query.Where("chat_id = ?", chatID)
	}
	var inputs []QueuedInput
	err := query.Find(&inputs).Error
	return inputs, err
}

func (s *Store) PendingInputs(ctx context.Context, limit int) ([]QueuedInput, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var inputs []QueuedInput
	err := s.db.WithContext(ctx).Where("status = ?", InputQueued).Order("created_at, id").Limit(limit).Find(&inputs).Error
	return inputs, err
}

func (s *Store) CancelQueuedInputs(ctx context.Context, chatID, branchID, reason string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if chatID == "" || branchID == "" || reason == "" || len(reason) > 64 {
		return errors.New("chat, branch, and a short cancellation reason are required")
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&QueuedInput{}).Where("chat_id = ? AND branch_id = ? AND status = ?", chatID, branchID, InputQueued).Updates(map[string]any{"status": InputFailed, "error_kind": reason, "finished_at": now}).Error
}
