package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

var ErrActiveTurn = errors.New("another generation is already active for this chat branch")
var ErrBranchNotFound = errors.New("runtime branch was not found")

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskBlocked   TaskStatus = "blocked"
	TaskUncertain TaskStatus = "uncertain"
)

type RecordStatus string

const (
	RecordPending   RecordStatus = "pending"
	RecordRunning   RecordStatus = "running"
	RecordCompleted RecordStatus = "completed"
	RecordFailed    RecordStatus = "failed"
	RecordCancelled RecordStatus = "cancelled"
	RecordUncertain RecordStatus = "uncertain"
)

type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepInProgress StepStatus = "in_progress"
	StepCompleted  StepStatus = "completed"
	StepBlocked    StepStatus = "blocked"
)

type Branch struct {
	ChatID            string    `gorm:"primaryKey;size:128" json:"chat_id"`
	BranchID          string    `gorm:"primaryKey;size:128" json:"branch_id"`
	ParentBranchID    string    `gorm:"size:128" json:"parent_branch_id,omitempty"`
	SessionID         string    `gorm:"size:256" json:"session_id"`
	CheckpointEventID string    `gorm:"size:128" json:"checkpoint_event_id,omitempty"`
	ProgressSourceID  string    `gorm:"size:128" json:"progress_source_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

func (Branch) TableName() string { return "runtime_branches" }

type BranchRevision struct {
	ID                string    `gorm:"primaryKey;size:128" json:"id"`
	ChatID            string    `gorm:"index:idx_runtime_branch_revision,priority:1;size:128" json:"chat_id"`
	BranchID          string    `gorm:"index:idx_runtime_branch_revision,priority:2;size:128" json:"branch_id"`
	SessionID         string    `gorm:"size:256" json:"session_id"`
	CheckpointEventID string    `gorm:"size:128" json:"checkpoint_event_id,omitempty"`
	Reason            string    `gorm:"size:32" json:"reason"`
	CreatedAt         time.Time `json:"created_at"`
}

func (BranchRevision) TableName() string { return "runtime_branch_revisions" }

type Task struct {
	ID            string     `gorm:"primaryKey;size:128" json:"generation_id"`
	ChatID        string     `gorm:"index:idx_runtime_chat_branch,priority:1;size:128" json:"chat_id"`
	BranchID      string     `gorm:"index:idx_runtime_chat_branch,priority:2;size:128" json:"branch_id"`
	Status        TaskStatus `gorm:"index:idx_runtime_chat_branch,priority:3" json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	ResultEventID string     `gorm:"size:128" json:"result_event_id,omitempty"`
	ErrorKind     string     `gorm:"size:64" json:"error_kind,omitempty"`
}

func (Task) TableName() string { return "runtime_tasks" }

type TaskRecord struct {
	ID         string       `gorm:"primaryKey;size:128" json:"record_id"`
	TaskID     string       `gorm:"index;size:128" json:"generation_id"`
	Kind       string       `gorm:"size:32" json:"kind"`
	Name       string       `gorm:"size:128" json:"name"`
	Status     RecordStatus `gorm:"index;size:32" json:"status"`
	StartedAt  time.Time    `json:"started_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
	ErrorKind  string       `gorm:"size:64" json:"error_kind,omitempty"`
}

func (TaskRecord) TableName() string { return "runtime_task_records" }

type ProgressStep struct {
	GenerationID string     `gorm:"primaryKey;size:128;index:idx_runtime_progress,priority:1" json:"generation_id"`
	StepID       string     `gorm:"primaryKey;size:128" json:"step_id"`
	ChatID       string     `gorm:"index:idx_runtime_progress,priority:2;size:128" json:"chat_id"`
	BranchID     string     `gorm:"index:idx_runtime_progress,priority:3;size:128" json:"branch_id"`
	Position     int        `json:"position"`
	Title        string     `gorm:"size:512" json:"title"`
	Status       StepStatus `gorm:"size:32" json:"status"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (ProgressStep) TableName() string { return "runtime_progress_steps" }

type ImportRun struct {
	ID            string    `gorm:"primaryKey;size:128" json:"id"`
	FormatVersion int       `json:"format_version"`
	Source        string    `gorm:"size:512" json:"source"`
	Imported      int       `json:"imported"`
	Skipped       int       `json:"skipped"`
	Failed        int       `json:"failed"`
	DryRun        bool      `json:"dry_run"`
	CreatedAt     time.Time `json:"created_at"`
}

func (ImportRun) TableName() string { return "runtime_import_runs" }

type InputKind string

const (
	InputSteering InputKind = "steering"
	InputFollowup InputKind = "followup"
	InputArticle  InputKind = "article"
)

type InputStatus string

const (
	InputQueued    InputStatus = "queued"
	InputClaimed   InputStatus = "claimed"
	InputCompleted InputStatus = "completed"
	InputFailed    InputStatus = "failed"
)

type QueuedInput struct {
	ID               string      `gorm:"primaryKey;size:128" json:"id"`
	ChatID           string      `gorm:"index:idx_runtime_input_queue,priority:1;size:128" json:"chat_id"`
	BranchID         string      `gorm:"index:idx_runtime_input_queue,priority:2;size:128" json:"branch_id"`
	UserID           string      `gorm:"size:128" json:"user_id,omitempty"`
	SenderID         int64       `json:"sender_id,omitempty"`
	UpdateID         int64       `gorm:"index" json:"update_id,omitempty"`
	ReplyToMessageID int64       `json:"reply_to_message_id,omitempty"`
	Kind             InputKind   `gorm:"size:32" json:"kind"`
	Status           InputStatus `gorm:"index:idx_runtime_input_queue,priority:3;size:32" json:"status"`
	Text             string      `gorm:"size:65536" json:"text"`
	AttachmentIDs    string      `gorm:"size:4096" json:"attachment_ids,omitempty"`
	SourceURLs       string      `gorm:"size:16384" json:"source_urls,omitempty"`
	PublishMorsel    bool        `json:"publish_morsel,omitempty"`
	PublishTitle     string      `gorm:"size:256" json:"publish_title,omitempty"`
	CreatedAt        time.Time   `gorm:"index:idx_runtime_input_queue,priority:4" json:"created_at"`
	ClaimedAt        *time.Time  `json:"claimed_at,omitempty"`
	FinishedAt       *time.Time  `json:"finished_at,omitempty"`
	ErrorKind        string      `gorm:"size:64" json:"error_kind,omitempty"`
}

func (QueuedInput) TableName() string { return "runtime_inputs" }

type Step struct {
	ID     string
	Title  string
	Status StepStatus
}

func (s *Store) UpdateCheckpoint(ctx context.Context, chatID, branchID, eventID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if chatID == "" || branchID == "" {
		return errors.New("chat and branch IDs are required")
	}
	branch := Branch{ChatID: chatID, BranchID: branchID, SessionID: defaultSessionID(chatID, branchID)}
	result := s.db.WithContext(ctx).Model(&Branch{}).Where("chat_id = ? AND branch_id = ?", chatID, branchID).Updates(map[string]any{"checkpoint_event_id": eventID, "session_id": gorm.Expr("CASE WHEN session_id = '' THEN ? ELSE session_id END", branch.SessionID)})
	if result.Error != nil {
		return fmt.Errorf("update branch checkpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		branch.CreatedAt = time.Now().UTC()
		if err := s.db.WithContext(ctx).Create(&branch).Error; err != nil {
			return fmt.Errorf("create branch checkpoint: %w", err)
		}
	}
	return nil
}

func defaultSessionID(chatID, branchID string) string {
	if branchID == "main" {
		return chatID
	}
	return chatID + "@" + branchID
}

func (s *Store) EnsureBranch(ctx context.Context, chatID, branchID string) (Branch, error) {
	if err := s.requireWrite(); err != nil {
		return Branch{}, err
	}
	if chatID == "" || branchID == "" {
		return Branch{}, errors.New("chat and branch IDs are required")
	}
	branch := Branch{ChatID: chatID, BranchID: branchID, SessionID: defaultSessionID(chatID, branchID), CreatedAt: time.Now().UTC()}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chat_id = ? AND branch_id = ?", chatID, branchID).First(&branch).Error; err == nil {
			if branch.SessionID == "" {
				branch.SessionID = defaultSessionID(chatID, branchID)
				return tx.Model(&Branch{}).Where("chat_id = ? AND branch_id = ?", chatID, branchID).Update("session_id", branch.SessionID).Error
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&branch).Error
	})
	if err != nil {
		return Branch{}, fmt.Errorf("ensure runtime branch: %w", err)
	}
	return branch, nil
}

func (s *Store) Branch(ctx context.Context, chatID, branchID string) (Branch, error) {
	var branch Branch
	err := s.db.WithContext(ctx).Where("chat_id = ? AND branch_id = ?", chatID, branchID).First(&branch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Branch{}, fmt.Errorf("%w: %q", ErrBranchNotFound, branchID)
	}
	return branch, err
}

func (s *Store) ListBranchRevisions(ctx context.Context, chatID, branchID string) ([]BranchRevision, error) {
	var revisions []BranchRevision
	err := s.db.WithContext(ctx).Where("chat_id = ? AND branch_id = ?", chatID, branchID).Order("created_at desc").Find(&revisions).Error
	return revisions, err
}

func (s *Store) ForkBranch(ctx context.Context, chatID, userID, parentID, branchID, checkpointID string) (Branch, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.requireWrite(); err != nil {
		return Branch{}, err
	}
	if chatID == "" || userID == "" || parentID == "" || branchID == "" || parentID == branchID {
		return Branch{}, errors.New("chat, user, distinct parent, and branch IDs are required")
	}
	var active int64
	if err := s.db.WithContext(ctx).Model(&Task{}).Where("chat_id = ? AND branch_id = ? AND status IN ?", chatID, parentID, []TaskStatus{TaskPending, TaskRunning}).Count(&active).Error; err != nil {
		return Branch{}, err
	}
	if active > 0 {
		return Branch{}, ErrActiveTurn
	}
	parent, err := s.Branch(ctx, chatID, parentID)
	if err != nil {
		return Branch{}, err
	}
	if checkpointID == "" {
		checkpointID = parent.CheckpointEventID
	}
	if checkpointID == "" {
		return Branch{}, errors.New("parent branch has no completed checkpoint")
	}
	var source session.Session
	var targetEvent int
	candidateIDs := []string{parent.SessionID}
	revisions, err := s.ListBranchRevisions(ctx, chatID, parentID)
	if err != nil {
		return Branch{}, err
	}
	for _, revision := range revisions {
		candidateIDs = append(candidateIDs, revision.SessionID)
	}
	for _, id := range candidateIDs {
		loaded, err := s.sessions.Get(ctx, &session.GetRequest{AppName: "abot", UserID: userID, SessionID: id})
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				continue
			}
			return Branch{}, fmt.Errorf("load branch history: %w", err)
		}
		for i := 0; i < loaded.Session.Events().Len(); i++ {
			if event := loaded.Session.Events().At(i); event != nil && event.ID == checkpointID {
				source, targetEvent = loaded.Session, i
				break
			}
		}
		if source != nil {
			break
		}
	}
	if source == nil {
		return Branch{}, fmt.Errorf("checkpoint event %q was not found in branch history", checkpointID)
	}
	branch := Branch{ChatID: chatID, BranchID: branchID, ParentBranchID: parentID, SessionID: defaultSessionID(chatID, branchID), CheckpointEventID: checkpointID, CreatedAt: time.Now().UTC()}
	var sourceTask Task
	var progress []ProgressStep
	taskErr := s.db.WithContext(ctx).Where("chat_id = ? AND branch_id = ? AND result_event_id = ?", chatID, parentID, checkpointID).Order("started_at desc").First(&sourceTask).Error
	if taskErr == nil {
		if err := s.db.WithContext(ctx).Where("generation_id = ?", sourceTask.ID).Order("position").Find(&progress).Error; err != nil {
			return Branch{}, err
		}
	} else if !errors.Is(taskErr, gorm.ErrRecordNotFound) {
		return Branch{}, taskErr
	}
	var copiedProgress []ProgressStep
	if len(progress) > 0 {
		branch.ProgressSourceID = "snapshot-" + uuid.NewString()
		for _, step := range progress {
			step.GenerationID = branch.ProgressSourceID
			step.ChatID, step.BranchID = chatID, branchID
			step.UpdatedAt = time.Now().UTC()
			copiedProgress = append(copiedProgress, step)
		}
	}
	if err := s.db.WithContext(ctx).Where("chat_id = ? AND branch_id = ?", chatID, branchID).First(&Branch{}).Error; err == nil {
		return Branch{}, fmt.Errorf("branch %q already exists", branchID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Branch{}, err
	}
	created, err := s.sessions.Create(ctx, &session.CreateRequest{AppName: "abot", UserID: userID, SessionID: branch.SessionID})
	if err != nil {
		return Branch{}, fmt.Errorf("create fork session: %w", err)
	}
	for i := 0; i <= targetEvent; i++ {
		original := source.Events().At(i)
		if original == nil {
			continue
		}
		copy := *original
		if err := s.sessions.AppendEvent(ctx, created.Session, &copy); err != nil {
			_ = s.sessions.Delete(ctx, &session.DeleteRequest{AppName: "abot", UserID: userID, SessionID: branch.SessionID})
			return Branch{}, fmt.Errorf("copy branch event %q: %w", original.ID, err)
		}
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&branch).Error; err != nil {
			return err
		}
		if len(copiedProgress) > 0 {
			return tx.Create(&copiedProgress).Error
		}
		return nil
	}); err != nil {
		_ = s.sessions.Delete(ctx, &session.DeleteRequest{AppName: "abot", UserID: userID, SessionID: branch.SessionID})
		return Branch{}, fmt.Errorf("save fork branch: %w", err)
	}
	return branch, nil
}

func (s *Store) CompactBranch(ctx context.Context, chatID, userID, branchID, summary string) (Branch, error) {
	ctx, span := otel.Tracer("abot/runtime").Start(ctx, "session.compact", trace.WithAttributes(attribute.String("chat.id", chatID), attribute.String("branch.id", branchID)))
	defer span.End()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.requireWrite(); err != nil {
		return Branch{}, err
	}
	if chatID == "" || userID == "" || branchID == "" || strings.TrimSpace(summary) == "" {
		return Branch{}, errors.New("chat, user, branch, and nonempty summary are required")
	}
	var active int64
	if err := s.db.WithContext(ctx).Model(&Task{}).Where("chat_id = ? AND branch_id = ? AND status IN ?", chatID, branchID, []TaskStatus{TaskPending, TaskRunning}).Count(&active).Error; err != nil {
		return Branch{}, err
	}
	if active > 0 {
		return Branch{}, ErrActiveTurn
	}
	branch, err := s.Branch(ctx, chatID, branchID)
	if errors.Is(err, ErrBranchNotFound) {
		branch, err = s.EnsureBranch(ctx, chatID, branchID)
		if err != nil {
			return Branch{}, err
		}
	} else if err != nil {
		return Branch{}, err
	}
	newSessionID := fmt.Sprintf("%s@%s@compact-%s", chatID, branchID, uuid.NewString())
	created, err := s.sessions.Create(ctx, &session.CreateRequest{AppName: "abot", UserID: userID, SessionID: newSessionID})
	if err != nil {
		return Branch{}, fmt.Errorf("create compacted ADK session: %w", err)
	}
	eventID := "compact-" + uuid.NewString()
	event := &session.Event{ID: eventID, Timestamp: time.Now().UTC(), Author: "system", Branch: branchID, LLMResponse: modelResponse(genai.NewContentFromText(strings.TrimSpace(summary), genai.RoleModel))}
	if err := s.sessions.AppendEvent(ctx, created.Session, event); err != nil {
		_ = s.sessions.Delete(ctx, &session.DeleteRequest{AppName: "abot", UserID: userID, SessionID: newSessionID})
		return Branch{}, fmt.Errorf("write compaction summary: %w", err)
	}
	oldSessionID := branch.SessionID
	if oldSessionID == "" {
		oldSessionID = defaultSessionID(chatID, branchID)
	}
	revision := BranchRevision{ID: uuid.NewString(), ChatID: chatID, BranchID: branchID, SessionID: oldSessionID, CheckpointEventID: branch.CheckpointEventID, Reason: "compaction", CreatedAt: time.Now().UTC()}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&revision).Error; err != nil {
			return fmt.Errorf("archive previous branch session: %w", err)
		}
		result := tx.Model(&Branch{}).Where("chat_id = ? AND branch_id = ?", chatID, branchID).Updates(map[string]any{"session_id": newSessionID, "checkpoint_event_id": eventID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Create(&Branch{ChatID: chatID, BranchID: branchID, SessionID: newSessionID, CheckpointEventID: eventID, CreatedAt: time.Now().UTC()}).Error
		}
		return nil
	})
	if err != nil {
		_ = s.sessions.Delete(ctx, &session.DeleteRequest{AppName: "abot", UserID: userID, SessionID: newSessionID})
		return Branch{}, fmt.Errorf("activate compacted branch: %w", err)
	}
	branch.SessionID, branch.CheckpointEventID = newSessionID, eventID
	return branch, nil
}

func (s *Store) Record(ctx context.Context, generationID, kind, name string) (string, error) {
	if err := s.requireWrite(); err != nil {
		return "", err
	}
	if kind == "" || name == "" {
		return "", errors.New("record kind and name are required")
	}
	var task Task
	if err := s.db.WithContext(ctx).First(&task, "id = ?", generationID).Error; err != nil {
		return "", fmt.Errorf("find generation: %w", err)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	record := TaskRecord{ID: id, TaskID: generationID, Kind: kind, Name: name, Status: RecordRunning, StartedAt: now, UpdatedAt: now}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return "", fmt.Errorf("record execution: %w", err)
	}
	return id, nil
}

func (s *Store) ToolObserver(generationID string) func(context.Context, string) (func(error) error, error) {
	return func(ctx context.Context, toolName string) (func(error) error, error) {
		_, span := otel.Tracer("abot/tools").Start(ctx, "tool.dispatch", trace.WithAttributes(attribute.String("tool.name", toolName), attribute.String("generation.id", generationID)))
		recordID, err := s.Record(ctx, generationID, "tool", toolName)
		if err != nil {
			span.SetStatus(codes.Error, "tool record could not be persisted")
			span.End()
			return nil, err
		}
		return func(toolErr error) error {
			status, kind := RecordCompleted, ""
			if errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded) {
				status, kind = RecordCancelled, "cancelled"
			} else if toolErr != nil {
				status, kind = RecordFailed, "tool_error"
				span.SetStatus(codes.Error, "tool dispatch failed")
				span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", toolErr)))
			}
			finishErr := s.FinishRecord(context.Background(), recordID, status, kind)
			if finishErr != nil {
				span.SetStatus(codes.Error, "tool record could not be finalized")
			}
			span.End()
			return finishErr
		}, nil
	}
}

func (s *Store) FinishRecord(ctx context.Context, recordID string, status RecordStatus, errorKind string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if status != RecordCompleted && status != RecordFailed && status != RecordCancelled {
		return fmt.Errorf("invalid terminal record status %q", status)
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&TaskRecord{}).Where("id = ? AND status IN ?", recordID, []RecordStatus{RecordPending, RecordRunning}).Updates(map[string]any{
		"status": status, "updated_at": now, "finished_at": now, "error_kind": errorKind,
	})
	if result.Error != nil {
		return fmt.Errorf("finish execution record: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("active record %q not found", recordID)
	}
	return nil
}

func (s *Store) SetProgress(ctx context.Context, chatID, branchID, generationID string, steps []Step) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	var task Task
	if err := s.db.WithContext(ctx).First(&task, "id = ?", generationID).Error; err != nil {
		return fmt.Errorf("find generation: %w", err)
	}
	seen := make(map[string]bool, len(steps))
	for _, step := range steps {
		if step.ID == "" || step.Title == "" || seen[step.ID] {
			return errors.New("progress steps require unique IDs and nonempty titles")
		}
		if step.Status != StepPending && step.Status != StepInProgress && step.Status != StepCompleted && step.Status != StepBlocked {
			return fmt.Errorf("invalid progress status %q", step.Status)
		}
		seen[step.ID] = true
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("generation_id = ?", generationID).Delete(&ProgressStep{}).Error; err != nil {
			return fmt.Errorf("replace progress steps: %w", err)
		}
		rows := make([]ProgressStep, 0, len(steps))
		for position, step := range steps {
			rows = append(rows, ProgressStep{GenerationID: generationID, StepID: step.ID, ChatID: chatID, BranchID: branchID, Position: position, Title: step.Title, Status: step.Status, UpdatedAt: time.Now().UTC()})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("save progress steps: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) ListBranches(ctx context.Context, chatID string) ([]Branch, error) {
	var branches []Branch
	err := s.db.WithContext(ctx).Where("chat_id = ?", chatID).Order("created_at, branch_id").Find(&branches).Error
	return branches, err
}

func (s *Store) ListTasks(ctx context.Context, chatID string, limit int) ([]Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := s.db.WithContext(ctx).Order("started_at desc").Limit(limit)
	if chatID != "" {
		query = query.Where("chat_id = ?", chatID)
	}
	var tasks []Task
	err := query.Find(&tasks).Error
	return tasks, err
}

func (s *Store) BranchProgress(ctx context.Context, chatID, branchID string) ([]ProgressStep, error) {
	branch, branchErr := s.Branch(ctx, chatID, branchID)
	if branchErr != nil && !errors.Is(branchErr, ErrBranchNotFound) {
		return nil, branchErr
	}
	var task Task
	err := s.db.WithContext(ctx).Where("chat_id = ? AND branch_id = ?", chatID, branchID).Order("started_at desc").First(&task).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var steps []ProgressStep
	if err == nil {
		err = s.db.WithContext(ctx).Where("generation_id = ?", task.ID).Order("position").Find(&steps).Error
		if err != nil || len(steps) > 0 {
			return steps, err
		}
	}
	if branch.ProgressSourceID != "" {
		err = s.db.WithContext(ctx).Where("generation_id = ?", branch.ProgressSourceID).Order("position").Find(&steps).Error
		return steps, err
	}
	return []ProgressStep{}, nil
}

func (s *Store) TaskDetails(ctx context.Context, generationID string) (Task, []TaskRecord, []ProgressStep, error) {
	var task Task
	if err := s.db.WithContext(ctx).First(&task, "id = ?", generationID).Error; err != nil {
		return Task{}, nil, nil, err
	}
	var records []TaskRecord
	if err := s.db.WithContext(ctx).Where("task_id = ?", generationID).Order("started_at").Find(&records).Error; err != nil {
		return Task{}, nil, nil, err
	}
	var steps []ProgressStep
	if err := s.db.WithContext(ctx).Where("generation_id = ?", generationID).Order("position").Find(&steps).Error; err != nil {
		return Task{}, nil, nil, err
	}
	return task, records, steps, nil
}
