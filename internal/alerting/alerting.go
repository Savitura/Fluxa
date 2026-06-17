package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type Level string

const (
	LevelCritical Level = "CRITICAL"
	LevelWarning  Level = "WARNING"
	LevelInfo     Level = "INFO"
)

type Alert struct {
	Level     Level   `json:"level"`
	Title     string  `json:"title"`
	Message   string  `json:"message"`
	Service   string  `json:"service"`
	Timestamp string  `json:"timestamp"`
}

type Client struct {
	webhookURL string
	httpClient *http.Client
	service    string
}

func NewClient(webhookURL, service string) *Client {
	return &Client{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		service:    service,
	}
}

func (c *Client) Send(ctx context.Context, level Level, title, message string) {
	if c.webhookURL == "" {
		log.Warn().Str("level", string(level)).Str("title", title).Msg("alerting: no webhook configured, alert skipped")
		return
	}

	alert := Alert{
		Level:     level,
		Title:     title,
		Message:   message,
		Service:   c.service,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(alert)
	if err != nil {
		log.Error().Err(err).Msg("alerting: marshal alert")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		log.Error().Err(err).Msg("alerting: create request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("alerting: send request")
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Error().Int("status", resp.StatusCode).Msg("alerting: webhook returned error")
		return
	}

	log.Info().Str("level", string(level)).Str("title", title).Msg("alerting: alert sent")
}

func (c *Client) Critical(ctx context.Context, title, message string) {
	c.Send(ctx, LevelCritical, title, message)
}

func (c *Client) Warning(ctx context.Context, title, message string) {
	c.Send(ctx, LevelWarning, title, message)
}

func (c *Client) Info(ctx context.Context, title, message string) {
	c.Send(ctx, LevelInfo, title, message)
}

func (c *Client) IsConfigured() bool {
	return c.webhookURL != ""
}

func (c *Client) Endpoint() string {
	return c.webhookURL
}

func (c *Client) Service() string {
	return c.service
}

func (c *Client) String() string {
	return fmt.Sprintf("alerting.Client{service=%s, webhook=%s}", c.service, c.webhookURL)
}
