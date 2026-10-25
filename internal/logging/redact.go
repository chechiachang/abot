package logging

import (
	"os"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

var (
	bearerPattern   = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/-]+=*`)
	userinfoPattern = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
	queryPattern    = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|key|token|access[_-]?token|sig(?:nature)?|password|secret|credential)=)[^&#\s]*`)
)

func New(secrets ...string) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	logger.SetFormatter(&logrus.TextFormatter{DisableColors: true, FullTimestamp: true})
	logger.AddHook(&redactionHook{secrets: secrets})
	return logger
}

func Redact(value string, secrets ...string) string {
	value = bearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = userinfoPattern.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = queryPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

type redactionHook struct {
	secrets []string
}

func (h *redactionHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *redactionHook) Fire(entry *logrus.Entry) error {
	entry.Message = Redact(entry.Message, h.secrets...)
	for key, value := range entry.Data {
		entry.Data[key] = redactValue(key, value, h.secrets)
	}
	return nil
}

func redactValue(key string, value any, secrets []string) any {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(key))
	for _, sensitive := range []string{"authorization", "api_key", "apikey", "token", "secret", "password", "credential"} {
		if strings.Contains(normalized, sensitive) {
			return "[REDACTED]"
		}
	}
	switch value := value.(type) {
	case string:
		return Redact(value, secrets...)
	case error:
		return Redact(value.Error(), secrets...)
	case map[string]any:
		for childKey, child := range value {
			value[childKey] = redactValue(childKey, child, secrets)
		}
		return value
	case map[string]string:
		for childKey, child := range value {
			value[childKey] = Redact(child, secrets...)
		}
		return value
	case []any:
		for i, child := range value {
			value[i] = redactValue("", child, secrets)
		}
		return value
	case []string:
		for i, child := range value {
			value[i] = Redact(child, secrets...)
		}
		return value
	default:
		return value
	}
}
