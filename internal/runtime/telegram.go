package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/session"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TelegramUpdate struct {
	UpdateID    int64     `gorm:"primaryKey" json:"update_id"`
	ChatID      int64     `gorm:"index" json:"chat_id"`
	Disposition string    `gorm:"size:32" json:"disposition"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type Attachment struct {
	ID              string    `gorm:"primaryKey;size:128" json:"id"`
	ChatID          string    `gorm:"index:idx_runtime_attachment,priority:1;size:128" json:"chat_id"`
	OwnerID         string    `gorm:"index:idx_runtime_attachment,priority:2;size:128" json:"owner_id,omitempty"`
	SourceMessageID int64     `gorm:"index:idx_runtime_attachment_source,priority:1" json:"source_message_id,omitempty"`
	IndexedChannel  bool      `gorm:"index" json:"indexed_channel,omitempty"`
	Path            string    `gorm:"size:2048" json:"path"`
	Name            string    `gorm:"size:256" json:"name"`
	MIMEType        string    `gorm:"size:128" json:"mime_type"`
	Kind            string    `gorm:"size:32" json:"kind"`
	Size            int64     `json:"size"`
	CreatedAt       time.Time `json:"created_at"`
}

func (Attachment) TableName() string { return "runtime_attachments" }

func (s *Store) SaveAttachment(ctx context.Context, item Attachment) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if item.ID == "" || item.ChatID == "" || item.Path == "" || item.Size <= 0 || item.MIMEType == "" || item.Kind == "" {
		return errors.New("attachment requires an ID, chat, private path, size, MIME type, and kind")
	}
	if item.IndexedChannel {
		if item.SourceMessageID <= 0 {
			return errors.New("indexed channel attachments require a source message")
		}
		item.OwnerID = ""
	} else if item.OwnerID == "" {
		return errors.New("user attachments require an owner")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Create(&item).Error
}

func (s *Store) ExpireAttachments(ctx context.Context, before time.Time) ([]Attachment, error) {
	if err := s.requireWrite(); err != nil {
		return nil, err
	}
	if before.IsZero() || before.After(time.Now().UTC()) {
		return nil, errors.New("attachment expiration cutoff must be in the past")
	}
	var expired []Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("created_at < ?", before.UTC()).Find(&expired).Error; err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		ids := make([]string, 0, len(expired))
		for _, item := range expired {
			ids = append(ids, item.ID)
		}
		return tx.Where("id IN ?", ids).Delete(&Attachment{}).Error
	})
	return expired, err
}

func (s *Store) AttachInput(ctx context.Context, inputID, chatID, ownerID string, ids []string, approvedChannels []int64) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 10 || inputID == "" || chatID == "" || ownerID == "" {
		return errors.New("attachment input references require an owner and at most 10 attachments")
	}
	encoded, err := json.Marshal(ids)
	if err != nil || len(encoded) > 4096 {
		return errors.New("attachment references exceed the queue limit")
	}
	var input QueuedInput
	if err := s.db.WithContext(ctx).Where("id = ? AND chat_id = ? AND status = ?", inputID, chatID, InputQueued).First(&input).Error; err != nil {
		return err
	}
	rows, err := s.authorizedAttachments(ctx, chatID, ownerID, ids, approvedChannels)
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return errors.New("one or more attachments are missing or not authorized for this chat")
	}
	return s.db.WithContext(ctx).Model(&QueuedInput{}).Where("id = ? AND status = ?", inputID, InputQueued).Update("attachment_ids", string(encoded)).Error
}

func (s *Store) SetArticleInput(ctx context.Context, inputID, title string, sourceURLs []string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if inputID == "" || strings.TrimSpace(title) == "" || len(title) > 256 || len(sourceURLs) > 4 {
		return errors.New("article input requires a title and at most four source URLs")
	}
	for _, raw := range sourceURLs {
		if strings.TrimSpace(raw) == "" || len(raw) > 2048 {
			return errors.New("article source URL is empty or too long")
		}
	}
	encoded, err := json.Marshal(sourceURLs)
	if err != nil || len(encoded) > 16<<10 {
		return errors.New("article source URLs exceed the storage limit")
	}
	result := s.db.WithContext(ctx).Model(&QueuedInput{}).Where("id = ? AND kind = ? AND status = ?", inputID, InputArticle, InputQueued).Updates(map[string]any{"publish_morsel": true, "publish_title": strings.TrimSpace(title), "source_urls": string(encoded)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("article input is no longer pending")
	}
	return nil
}

func (s *Store) InputAttachments(ctx context.Context, inputID, chatID, ownerID string, approvedChannels []int64) ([]Attachment, error) {
	var input QueuedInput
	if err := s.db.WithContext(ctx).Where("id = ? AND chat_id = ?", inputID, chatID).First(&input).Error; err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.AttachmentIDs) == "" {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(input.AttachmentIDs), &ids); err != nil {
		return nil, errors.New("queued attachment references are invalid")
	}
	rows, err := s.authorizedAttachments(ctx, chatID, ownerID, ids, approvedChannels)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, errors.New("one or more queued attachments are missing or no longer authorized")
	}
	return rows, nil
}

func (s *Store) AttachmentsForChannel(ctx context.Context, channelID, messageID int64) ([]Attachment, error) {
	if channelID >= 0 || messageID <= 0 {
		return nil, errors.New("channel attachment lookup requires a negative channel and source message")
	}
	var rows []Attachment
	err := s.db.WithContext(ctx).Where("chat_id = ? AND source_message_id = ? AND indexed_channel = ?", strconv.FormatInt(channelID, 10), messageID, true).Order("created_at, id").Find(&rows).Error
	return rows, err
}

func (s *Store) RemoveAttachments(ctx context.Context, ids []string, chatID, ownerID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Where("id IN ? AND chat_id = ? AND owner_id = ? AND indexed_channel = ?", ids, chatID, ownerID, false).Delete(&Attachment{}).Error
}

func (s *Store) FailQueuedInput(ctx context.Context, inputID, errorKind string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if errorKind == "" || len(errorKind) > 64 {
		return errors.New("queued-input failure kind is required")
	}
	result := s.db.WithContext(ctx).Model(&QueuedInput{}).Where("id = ? AND status = ?", inputID, InputQueued).Updates(map[string]any{"status": InputFailed, "error_kind": errorKind, "finished_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("queued input is no longer pending")
	}
	return nil
}

func (s *Store) authorizedAttachments(ctx context.Context, chatID, ownerID string, ids []string, approvedChannels []int64) ([]Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Attachment
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]Attachment, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	result := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		row, exists := byID[id]
		if !exists {
			continue
		}
		if row.ChatID == chatID && row.OwnerID == ownerID && !row.IndexedChannel {
			result = append(result, row)
			continue
		}
		channelID, err := strconv.ParseInt(row.ChatID, 10, 64)
		if err == nil && row.IndexedChannel && slices.Contains(approvedChannels, channelID) {
			result = append(result, row)
		}
	}
	return result, nil
}

func (TelegramUpdate) TableName() string { return "runtime_telegram_updates" }

type RuntimeValue struct {
	Key       string `gorm:"primaryKey;size:64"`
	IntValue  int64
	UpdatedAt time.Time
}

func (RuntimeValue) TableName() string { return "runtime_values" }

type PassiveMessage struct {
	ID        string    `gorm:"primaryKey;size:128" json:"id"`
	UpdateID  int64     `gorm:"uniqueIndex" json:"update_id"`
	ChatID    int64     `gorm:"index" json:"chat_id"`
	UserID    int64     `json:"user_id"`
	MessageID int64     `json:"message_id"`
	Text      string    `gorm:"size:8192" json:"text"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

func (PassiveMessage) TableName() string { return "runtime_passive_messages" }

type OutboxStatus string

const (
	OutboxPending OutboxStatus = "pending"
	OutboxSending OutboxStatus = "sending"
	OutboxSent    OutboxStatus = "sent"
	OutboxFailed  OutboxStatus = "failed"
)

type Outbox struct {
	R2ObjectLocator    string       `gorm:"size:2048" json:"-"`
	R2URLExpirySeconds int64        `json:"-"`
	R2ObjectExpiresAt  time.Time    `json:"-"`
	ID                 string       `gorm:"primaryKey;size:128" json:"id"`
	ChatID             int64        `gorm:"index:idx_runtime_outbox,priority:1;index:idx_runtime_outbox_chat_message,priority:1" json:"chat_id"`
	UserID             string       `gorm:"size:128" json:"user_id"`
	BranchID           string       `gorm:"size:128" json:"branch_id"`
	CheckpointEventID  string       `gorm:"size:128" json:"checkpoint_event_id"`
	Payload            string       `gorm:"size:4194304" json:"payload"`
	ParseMode          string       `gorm:"size:16" json:"parse_mode"`
	ReplyToMessageID   int64        `json:"reply_to_message_id,omitempty"`
	Status             OutboxStatus `gorm:"index:idx_runtime_outbox,priority:2;size:32" json:"status"`
	Attempts           int          `json:"attempts"`
	NextAttemptAt      time.Time    `gorm:"index:idx_runtime_outbox,priority:3" json:"next_attempt_at"`
	TelegramMessageID  *int64       `gorm:"index:idx_runtime_outbox_chat_message,priority:2" json:"telegram_message_id,omitempty"`
	ErrorKind          string       `gorm:"size:64" json:"error_kind,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	SentAt             *time.Time   `json:"sent_at,omitempty"`
}

func (Outbox) TableName() string { return "runtime_telegram_outbox" }

type AccessRequest struct {
	ID         string     `gorm:"primaryKey;size:128" json:"id"`
	ChatID     int64      `gorm:"index:idx_runtime_access,priority:1" json:"chat_id"`
	UserID     int64      `gorm:"index:idx_runtime_access,priority:2" json:"user_id"`
	ChatType   string     `gorm:"size:32" json:"chat_type"`
	Status     string     `gorm:"index;size:32" json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy string     `gorm:"size:128" json:"reviewed_by,omitempty"`
}

func (AccessRequest) TableName() string { return "runtime_telegram_access_requests" }

type Conversation struct {
	ChatID         string    `gorm:"primaryKey;size:128" json:"chat_id"`
	ActiveBranchID string    `gorm:"size:128" json:"active_branch_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (Conversation) TableName() string { return "runtime_conversations" }

func (s *Store) RecordTelegramUpdate(ctx context.Context, updateID, chatID int64, disposition string) (bool, error) {
	if err := s.requireWrite(); err != nil {
		return false, err
	}
	if updateID <= 0 || disposition == "" || len(disposition) > 32 {
		return false, errors.New("telegram update ID and disposition are required")
	}
	accepted := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TelegramUpdate{UpdateID: updateID, ChatID: chatID, Disposition: disposition, RecordedAt: time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		accepted = result.RowsAffected == 1
		if !accepted {
			return nil
		}
		var offset RuntimeValue
		err := tx.First(&offset, "key = ?", "telegram_update_offset").Error
		if errors.Is(err, gorm.ErrRecordNotFound) || updateID+1 > offset.IntValue {
			return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.Assignments(map[string]any{"int_value": updateID + 1, "updated_at": time.Now().UTC()})}).Create(&RuntimeValue{Key: "telegram_update_offset", IntValue: updateID + 1, UpdatedAt: time.Now().UTC()}).Error
		}
		return err
	})
	if err != nil {
		return false, fmt.Errorf("record telegram update: %w", err)
	}
	return accepted, nil
}

func (s *Store) TelegramUpdateOffset(ctx context.Context) (int64, error) {
	var value RuntimeValue
	err := s.db.WithContext(ctx).First(&value, "key = ?", "telegram_update_offset").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return value.IntValue, err
}

func (s *Store) SetTelegramUpdateOffset(ctx context.Context, offset int64) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if offset < 0 {
		return errors.New("telegram update offset cannot be negative")
	}
	value := RuntimeValue{Key: "telegram_update_offset", IntValue: offset, UpdatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"int_value": offset, "updated_at": value.UpdatedAt}),
	}).Create(&value).Error; err != nil {
		return fmt.Errorf("set Telegram update offset: %w", err)
	}
	return nil
}

func (s *Store) SavePassiveMessage(ctx context.Context, message PassiveMessage) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	message.Text = strings.TrimSpace(message.Text)
	if message.UpdateID <= 0 || message.ChatID >= 0 || message.UserID <= 0 || message.Text == "" {
		return errors.New("passive group context requires an update, negative group, allowed sender, and text")
	}
	if len(message.Text) > 8<<10 {
		message.Text = strings.ToValidUTF8(message.Text[:8<<10], "")
	}
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "update_id"}}, DoNothing: true}).Create(&message).Error; err != nil {
		return fmt.Errorf("save passive group context: %w", err)
	}
	return s.db.WithContext(ctx).Exec(`DELETE FROM runtime_passive_messages WHERE chat_id = ? AND id NOT IN (SELECT id FROM runtime_passive_messages WHERE chat_id = ? ORDER BY created_at DESC LIMIT 200)`, message.ChatID, message.ChatID).Error
}

func (s *Store) RecentPassiveMessages(ctx context.Context, chatID int64, limit int) ([]PassiveMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var messages []PassiveMessage
	err := s.db.WithContext(ctx).Where("chat_id = ?", chatID).Order("created_at desc").Limit(limit).Find(&messages).Error
	return messages, err
}

func (s *Store) EnqueueOutbox(ctx context.Context, item Outbox) (Outbox, error) {
	if err := s.requireWrite(); err != nil {
		return Outbox{}, err
	}
	if item.ChatID == 0 || item.Payload == "" || len(item.Payload) > 4<<20 {
		return Outbox{}, errors.New("outbound chat and payload of 1 byte to 4 MiB are required")
	}
	if item.R2ObjectLocator != "" {
		if len(item.R2ObjectLocator) > 2048 || item.R2URLExpirySeconds < 1 || item.R2URLExpirySeconds >= 86400 || item.R2ObjectExpiresAt.IsZero() || item.R2ObjectExpiresAt.After(time.Now().Add(24*time.Hour)) {
			return Outbox{}, errors.New("invalid R2 outbound object metadata")
		}
	} else if item.R2URLExpirySeconds != 0 || !item.R2ObjectExpiresAt.IsZero() {
		return Outbox{}, errors.New("R2 outbound metadata requires an object locator")
	}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.Status, item.Attempts = OutboxPending, 0
	item.CreatedAt, item.UpdatedAt, item.NextAttemptAt = time.Now().UTC(), time.Now().UTC(), time.Now().UTC()
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return Outbox{}, fmt.Errorf("persist outbound delivery: %w", err)
	}
	return item, nil
}

func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]Outbox, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var items []Outbox
	err := s.db.WithContext(ctx).Where("status = ? AND next_attempt_at <= ?", OutboxPending, time.Now().UTC()).Order("created_at, id").Limit(limit).Find(&items).Error
	return items, err
}

func (s *Store) BeginDelivery(ctx context.Context, id string) (bool, error) {
	if err := s.requireWrite(); err != nil {
		return false, err
	}
	result := s.db.WithContext(ctx).Model(&Outbox{}).Where("id = ? AND status = ? AND next_attempt_at <= ?", id, OutboxPending, time.Now().UTC()).Updates(map[string]any{"status": OutboxSending, "attempts": gorm.Expr("attempts + 1"), "updated_at": time.Now().UTC()})
	return result.RowsAffected == 1, result.Error
}

func (s *Store) CompleteDelivery(ctx context.Context, id string, messageID int64) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&Outbox{}).Where("id = ? AND status = ?", id, OutboxSending).Updates(map[string]any{"status": OutboxSent, "telegram_message_id": messageID, "sent_at": now, "updated_at": now, "error_kind": ""})
	if result.Error != nil {
		return fmt.Errorf("complete outbound delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("sending outbox item %q not found", id)
	}
	return nil
}

func (s *Store) FailDelivery(ctx context.Context, id, errorKind string, retryAt time.Time) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	status := OutboxFailed
	if !retryAt.IsZero() {
		status = OutboxPending
	}
	result := s.db.WithContext(ctx).Model(&Outbox{}).Where("id = ? AND status = ?", id, OutboxSending).Updates(map[string]any{"status": status, "error_kind": errorKind, "next_attempt_at": retryAt.UTC(), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("record outbound delivery failure: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("sending outbox item %q not found", id)
	}
	return nil
}

func (s *Store) OutboxByMessageID(ctx context.Context, chatID, messageID int64) (Outbox, error) {
	var item Outbox
	err := s.db.WithContext(ctx).Where("chat_id = ? AND telegram_message_id = ? AND status = ?", chatID, messageID, OutboxSent).Order("sent_at DESC, created_at DESC, id DESC").First(&item).Error
	return item, err
}

func (s *Store) RequestAccess(ctx context.Context, chatID, userID int64, chatType string) (AccessRequest, bool, error) {
	if err := s.requireWrite(); err != nil {
		return AccessRequest{}, false, err
	}
	if chatID == 0 || userID <= 0 || chatType == "" {
		return AccessRequest{}, false, errors.New("access request requires chat type and concrete chat/sender IDs")
	}
	var existing AccessRequest
	err := s.db.WithContext(ctx).Where("chat_id = ? AND user_id = ? AND status = ?", chatID, userID, "pending").First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return AccessRequest{}, false, err
	}
	request := AccessRequest{ID: uuid.NewString(), ChatID: chatID, UserID: userID, ChatType: chatType, Status: "pending", CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(&request).Error; err != nil {
		return AccessRequest{}, false, fmt.Errorf("save access request: %w", err)
	}
	return request, true, nil
}

func (s *Store) ListAccessRequests(ctx context.Context, status string) ([]AccessRequest, error) {
	query := s.db.WithContext(ctx).Order("created_at desc").Limit(500)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var requests []AccessRequest
	err := query.Find(&requests).Error
	return requests, err
}

func (s *Store) ReviewAccessRequest(ctx context.Context, id, admin string, approve bool) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&AccessRequest{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]any{"status": status, "reviewed_at": now, "reviewed_by": admin})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("pending access request not found")
	}
	return nil
}

func (s *Store) CurrentBranch(ctx context.Context, chatID string) (string, error) {
	var conversation Conversation
	err := s.db.WithContext(ctx).First(&conversation, "chat_id = ?", chatID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "main", nil
	}
	return conversation.ActiveBranchID, err
}

func (s *Store) ActivateBranch(ctx context.Context, chatID, branchID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if chatID == "" || branchID == "" {
		return errors.New("chat and branch IDs are required")
	}
	conversation := Conversation{ChatID: chatID, ActiveBranchID: branchID, UpdatedAt: time.Now().UTC()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "chat_id"}}, DoUpdates: clause.Assignments(map[string]any{"active_branch_id": branchID, "updated_at": conversation.UpdatedAt})}).Create(&conversation).Error
}

func (s *Store) ResetBranch(ctx context.Context, chatID, userID string) (Branch, error) {
	if err := s.requireWrite(); err != nil {
		return Branch{}, err
	}
	parentID, err := s.CurrentBranch(ctx, chatID)
	if err != nil {
		return Branch{}, err
	}
	branchID := "reset-" + uuid.NewString()
	branch := Branch{ChatID: chatID, BranchID: branchID, ParentBranchID: parentID, SessionID: defaultSessionID(chatID, branchID), CreatedAt: time.Now().UTC()}
	if _, err := s.sessions.Create(ctx, &session.CreateRequest{AppName: "abot", UserID: userID, SessionID: branch.SessionID}); err != nil {
		return Branch{}, fmt.Errorf("create reset conversation session: %w", err)
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&QueuedInput{}).Where("chat_id = ? AND branch_id = ? AND status = ?", chatID, parentID, InputQueued).Updates(map[string]any{"status": InputFailed, "error_kind": "reset", "finished_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := tx.Create(&branch).Error; err != nil {
			return err
		}
		conversation := Conversation{ChatID: chatID, ActiveBranchID: branchID, UpdatedAt: time.Now().UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "chat_id"}}, DoUpdates: clause.Assignments(map[string]any{"active_branch_id": branchID, "updated_at": conversation.UpdatedAt})}).Create(&conversation).Error
	}); err != nil {
		_ = s.sessions.Delete(ctx, &session.DeleteRequest{AppName: "abot", UserID: userID, SessionID: branch.SessionID})
		return Branch{}, fmt.Errorf("save reset conversation: %w", err)
	}
	return branch, nil
}

func (s *Store) RecoverTelegramDelivery(ctx context.Context) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&Outbox{}).Where("status = ?", OutboxSending).Updates(map[string]any{"status": OutboxPending, "next_attempt_at": time.Now().UTC()}).Error
}
