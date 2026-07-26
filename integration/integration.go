// Package integration adapts caller-owned startup and shutdown hooks into the
// service lifecycle without owning their implementations or providers.
package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/faustbrian/golib/pkg/service/service"
)

const maximumLogAttributes = 32

// ErrInvalidConfig identifies invalid integration configuration.
var ErrInvalidConfig = errors.New("invalid service integration configuration")

// ConfigError identifies one invalid integration field.
type ConfigError struct {
	// Field identifies the rejected configuration path.
	Field string
	// Reason describes why Field was rejected.
	Reason string
}

// Error implements error.
func (err *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s: %v", err.Field, err.Reason, ErrInvalidConfig)
}

// Unwrap makes ConfigError inspectable with errors.Is.
func (err *ConfigError) Unwrap() error {
	return ErrInvalidConfig
}

// Hook is caller-owned lifecycle work.
type Hook func(context.Context) error

// Hooks contains optional caller-owned startup and shutdown functions.
type Hooks struct {
	// Start performs caller-owned startup work. Nil is a valid no-op.
	Start Hook
	// Stop reverses a successful Start. Nil is a valid no-op.
	Stop Hook
}

type config struct {
	logger     *slog.Logger
	loggerSet  bool
	attributes []slog.Attr
}

// Option configures an integration component.
type Option func(*config) error

// WithSlog reports hook lifecycle status through a caller-owned logger. The
// logger handler is never closed, replaced, or otherwise owned by this package.
func WithSlog(logger *slog.Logger, attributes ...slog.Attr) Option {
	return func(config *config) error {
		if logger == nil {
			return &ConfigError{Field: "logger", Reason: "must not be nil"}
		}
		if config.loggerSet {
			return &ConfigError{Field: "logger", Reason: "must be configured once"}
		}
		if len(attributes) > maximumLogAttributes {
			return &ConfigError{Field: "attributes", Reason: "exceeds hard limit"}
		}
		for index, attribute := range attributes {
			if strings.TrimSpace(attribute.Key) == "" {
				return &ConfigError{
					Field:  fmt.Sprintf("attributes[%d].Key", index),
					Reason: "must not be blank",
				}
			}
		}
		config.logger = logger
		config.loggerSet = true
		config.attributes = append([]slog.Attr(nil), attributes...)

		return nil
	}
}

// New adapts hooks into one ordered service component. A hook error is returned
// unchanged for errors.Is and errors.As policy and is not logged automatically.
func New(name string, hooks Hooks, options ...Option) (service.Component, error) {
	if strings.TrimSpace(name) == "" {
		return service.Component{}, &ConfigError{Field: "name", Reason: "must not be blank"}
	}

	configured := config{}
	for index, option := range options {
		if option == nil {
			return service.Component{}, &ConfigError{
				Field:  fmt.Sprintf("options[%d]", index),
				Reason: "must not be nil",
			}
		}
		if err := option(&configured); err != nil {
			return service.Component{}, err
		}
	}

	return service.Component{
		Name: name,
		Start: func(ctx context.Context) error {
			logStatus(ctx, configured, name, "integration starting")
			if hooks.Start == nil {
				logStatus(ctx, configured, name, "integration started")

				return nil
			}
			if err := hooks.Start(ctx); err != nil {
				logStatus(ctx, configured, name, "integration start failed")

				return err
			}
			logStatus(ctx, configured, name, "integration started")

			return nil
		},
		Stop: func(ctx context.Context) error {
			logStatus(ctx, configured, name, "integration stopping")
			if hooks.Stop == nil {
				logStatus(ctx, configured, name, "integration stopped")

				return nil
			}
			if err := hooks.Stop(ctx); err != nil {
				logStatus(ctx, configured, name, "integration stop failed")

				return err
			}
			logStatus(ctx, configured, name, "integration stopped")

			return nil
		},
	}, nil
}

func logStatus(ctx context.Context, config config, name, message string) {
	if config.logger == nil {
		return
	}

	attributes := make([]slog.Attr, 0, len(config.attributes)+1)
	attributes = append(attributes, slog.String("component", name))
	attributes = append(attributes, config.attributes...)
	config.logger.LogAttrs(ctx, slog.LevelInfo, message, attributes...)
}
