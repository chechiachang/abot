package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

const apiBaseURL = "https://api.telegram.org"

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

type APIError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram Bot API error (code %d)", e.Code)
}

func (e *APIError) Fatal() bool {
	return e.Code == http.StatusBadRequest || e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden || e.Code == http.StatusConflict
}

func NewClient(token string) (*Client, error) {
	return NewClientWithHTTP(token, apiBaseURL, &http.Client{Timeout: 60 * time.Second})
}

func NewClientWithHTTP(token, baseURL string, client *http.Client) (*Client, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(baseURL) == "" || client == nil {
		return nil, errors.New("Telegram token, API base URL, and HTTP client are required")
	}
	return &Client{token: token, baseURL: strings.TrimRight(baseURL, "/"), httpClient: client}, nil
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type Message struct {
	MessageID      int64       `json:"message_id"`
	Date           int64       `json:"date"`
	Chat           Chat        `json:"chat"`
	From           *User       `json:"from,omitempty"`
	Text           string      `json:"text,omitempty"`
	Caption        string      `json:"caption,omitempty"`
	Photo          []PhotoSize `json:"photo,omitempty"`
	Document       *Document   `json:"document,omitempty"`
	Voice          *AudioFile  `json:"voice,omitempty"`
	Audio          *AudioFile  `json:"audio,omitempty"`
	Entities       []Entity    `json:"entities,omitempty"`
	ReplyToMessage *Message    `json:"reply_to_message,omitempty"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type AudioFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	Duration int    `json:"duration"`
	FileSize int64  `json:"file_size"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type AnswerCallbackQuery struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	ChannelPost   *Message       `json:"channel_post,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type TelegramFile struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	FilePath string `json:"file_path"`
}

type Updates struct {
	Offset  int64 `json:"offset,omitempty"`
	Timeout int   `json:"timeout"`
}

type SendMessage struct {
	ChatID             int64                 `json:"chat_id"`
	Text               string                `json:"text"`
	ParseMode          string                `json:"parse_mode,omitempty"`
	ReplyToMessageID   int64                 `json:"reply_to_message_id,omitempty"`
	DisableLinkPreview bool                  `json:"disable_web_page_preview,omitempty"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type SentMessage struct {
	MessageID int64 `json:"message_id"`
	Chat      Chat  `json:"chat"`
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, answer AnswerCallbackQuery) error {
	return c.call(ctx, "answerCallbackQuery", answer, nil)
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var user User
	err := c.call(ctx, "getMe", struct{}{}, &user)
	return user, err
}

func (c *Client) GetFile(ctx context.Context, fileID string) (TelegramFile, error) {
	var file TelegramFile
	if strings.TrimSpace(fileID) == "" {
		return file, errors.New("Telegram file ID is required")
	}
	err := c.call(ctx, "getFile", struct {
		FileID string `json:"file_id"`
	}{FileID: fileID}, &file)
	if err != nil {
		return TelegramFile{}, err
	}
	if file.FilePath == "" {
		return TelegramFile{}, errors.New("Telegram file response omitted its path")
	}
	return file, nil
}

func (c *Client) DownloadFile(ctx context.Context, filePath string, maxBytes int64) ([]byte, error) {
	clean := path.Clean(filePath)
	if clean != filePath || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.ContainsAny(clean, "?#\\") {
		return nil, errors.New("Telegram returned an invalid file path")
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return nil, errors.New("Telegram returned an invalid file path")
		}
	}
	if maxBytes <= 0 {
		return nil, errors.New("Telegram download size limit must be positive")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+clean, nil)
	if err != nil {
		return nil, errors.New("create Telegram file request")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		safeErr := strings.ReplaceAll(err.Error(), c.token, "[REDACTED]")
		return nil, fmt.Errorf("download Telegram file: %s", safeErr)
	}
	defer response.Body.Close()
	if !responseSuccess(response.StatusCode) {
		return nil, fmt.Errorf("Telegram file download failed (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("read Telegram file download")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("Telegram file exceeds the configured size limit")
	}
	return data, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	if timeout < 0 || timeout > 50 {
		return nil, errors.New("Telegram polling timeout must be between 0 and 50 seconds")
	}
	var updates []Update
	err := c.call(ctx, "getUpdates", Updates{Offset: offset, Timeout: timeout}, &updates)
	return updates, err
}

func (c *Client) SendMessage(ctx context.Context, message SendMessage) (SentMessage, error) {
	var sent SentMessage
	err := c.call(ctx, "sendMessage", message, &sent)
	return sent, err
}

type editMessage struct {
	ChatID             int64  `json:"chat_id"`
	MessageID          int64  `json:"message_id"`
	Text               string `json:"text"`
	ParseMode          string `json:"parse_mode,omitempty"`
	DisableLinkPreview bool   `json:"disable_web_page_preview,omitempty"`
}

func (c *Client) EditMessage(ctx context.Context, message SendMessage, messageID int64) error {
	return c.call(ctx, "editMessageText", editMessage{ChatID: message.ChatID, MessageID: messageID, Text: message.Text, ParseMode: message.ParseMode, DisableLinkPreview: message.DisableLinkPreview}, nil)
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode Telegram request: %w", err)
	}
	endpoint := c.baseURL + "/bot" + c.token + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create Telegram API request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		safeErr := strings.ReplaceAll(err.Error(), c.token, "[REDACTED]")
		return fmt.Errorf("call Telegram Bot API: %s", safeErr)
	}
	defer response.Body.Close()
	var payload struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		ErrorCode  int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&payload); err != nil {
		return fmt.Errorf("decode Telegram Bot API response (HTTP %d)", response.StatusCode)
	}
	if !responseSuccess(response.StatusCode) || !payload.OK {
		code := payload.ErrorCode
		if code == 0 {
			code = response.StatusCode
		}
		return &APIError{Code: code, RetryAfter: time.Duration(payload.Parameters.RetryAfter) * time.Second}
	}
	if result != nil {
		if err := json.Unmarshal(payload.Result, result); err != nil {
			return fmt.Errorf("decode Telegram %s result: %w", method, err)
		}
	}
	return nil
}

func responseSuccess(code int) bool { return code >= 200 && code < 300 }

func retryDelay(err error, attempt int) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Fatal() {
			return 0, false
		}
		if apiErr.RetryAfter > 0 {
			return apiErr.RetryAfter, true
		}
	}
	delay := time.Second * time.Duration(1<<min(attempt, 5))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay, true
}
