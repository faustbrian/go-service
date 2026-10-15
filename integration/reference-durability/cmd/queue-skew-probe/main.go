// Package main supplies the public Queue common-fields consumer used by the
// isolated released-version compatibility campaign.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	queue "github.com/faustbrian/go-queue"
	"github.com/faustbrian/go-queue/core"
	"github.com/faustbrian/go-queue/job"
	"github.com/faustbrian/go-queue/valkeystream"
)

type payload struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}
type rawPayload []byte

func (value rawPayload) Bytes() []byte { return value }

type report struct {
	QueueVersion string   `json:"queue_version"`
	IDs          []string `json:"ids"`
	Acknowledged uint64   `json:"acknowledged"`
	Reclaimed    uint64   `json:"reclaimed"`
	Pending      int64    `json:"pending"`
	Lag          int64    `json:"lag"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verifyQueueVersion(info *debug.BuildInfo, expected string) error {
	if info != nil && (expected == "v1.1.0" || expected == "v1.1.2") {
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/faustbrian/go-queue" && dependency.Version == expected && dependency.Replace == nil {
				return nil
			}
		}
	}
	return errors.New("queue skew: exact public Queue build identity required; replacements or MVS drift are forbidden")
}

func commonMessage(id string) *job.Message {
	body, _ := json.Marshal(payload{ID: id, Value: "queue-common-v1"})
	message := job.NewMessage(rawPayload(body), job.AllowOption{Timeout: job.Time(2 * time.Second)})
	return &message
}

func validateMessage(message core.TaskMessage, prefix string) (string, error) {
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(message.Payload()), 4097))
	decoder.DisallowUnknownFields()
	var value payload
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("queue skew: trailing payload")
	}
	if !strings.HasPrefix(value.ID, prefix+"-") || value.Value != "queue-common-v1" || !bytes.Equal(message.Bytes(), commonMessage(value.ID).Bytes()) {
		return "", errors.New("queue skew: common payload or execution fields changed")
	}
	return value.ID, nil
}

func run(parent context.Context, arguments []string, output io.Writer) (err error) {
	flags := flag.NewFlagSet("queue-skew-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var mode, version, address, stream, consumer, prefix, ready, release string
	var count, first int
	var reclaim bool
	var timeout time.Duration
	flags.StringVar(&mode, "mode", "", "identity, publish, hold, consume, compete, verify-ack")
	flags.StringVar(&version, "queue-version", "", "required exact public Queue version")
	flags.StringVar(&address, "address", "", "task-owned Valkey address")
	flags.StringVar(&stream, "stream", "", "task-owned stream")
	flags.StringVar(&consumer, "consumer", "probe", "consumer identity")
	flags.StringVar(&prefix, "prefix", "", "common task cohort")
	flags.StringVar(&ready, "ready", "", "task-owned readiness JSON file")
	flags.StringVar(&release, "release", "", "task-owned mixed-consumer release file")
	flags.IntVar(&count, "count", 1, "maximum task count, 1 through 8")
	flags.IntVar(&first, "first", 0, "first published task index")
	flags.BoolVar(&reclaim, "reclaim", false, "reclaim only after the prior owner has stopped")
	flags.DurationVar(&timeout, "timeout", 10*time.Second, "bounded process operation")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || count < 1 || count > 8 || first < 0 || first+count > 8 || timeout <= 0 || timeout > 30*time.Second {
		return errors.New("queue skew: invalid bounds")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("queue skew: missing build information")
	}
	if err := verifyQueueVersion(info, version); err != nil {
		return err
	}
	result := report{QueueVersion: version, IDs: []string{}}
	if mode == "identity" {
		return json.NewEncoder(output).Encode(result)
	}
	if address == "" || stream == "" || consumer == "" || prefix == "" || len(prefix) > 64 {
		return errors.New("queue skew: missing scenario boundary")
	}
	if mode != "publish" && mode != "hold" && mode != "consume" && mode != "compete" && mode != "verify-ack" {
		return errors.New("queue skew: invalid mode")
	}
	if (mode == "hold" || mode == "compete") && ready == "" {
		return errors.New("queue skew: ready file required")
	}
	if mode == "compete" && (release == "" || reclaim) {
		return errors.New("queue skew: live competition requires release barrier and disabled reclaim")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	options := []valkeystream.Option{
		valkeystream.WithAddress(address), valkeystream.WithStreamName(stream),
		valkeystream.WithGroup("released-queue-skew"), valkeystream.WithConsumer(consumer),
		valkeystream.WithMaxLength(128), valkeystream.WithReadBatchSize(1),
		valkeystream.WithRequestTimeout(500 * time.Millisecond), valkeystream.WithBlockTime(100 * time.Millisecond),
		valkeystream.WithDialTimeout(time.Second), valkeystream.WithCommandTimeout(time.Second), valkeystream.WithShutdownTimeout(2 * time.Second),
		valkeystream.WithReclaim(time.Hour, time.Hour, 1),
	}
	if reclaim {
		options = append(options, valkeystream.WithReclaim(100*time.Millisecond, 100*time.Millisecond, 1), valkeystream.WithRequestTimeout(3*time.Second))
	}
	if mode == "publish" {
		publisher, err := valkeystream.NewPublisherE(options...)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, publisher.Shutdown()) }()
		for index := first; index < first+count; index++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := fmt.Sprintf("%s-%d", prefix, index)
			if err := publisher.Queue(rawPayload(commonMessage(id).Payload()), job.AllowOption{Timeout: job.Time(2 * time.Second)}); err != nil {
				return err
			}
			result.IDs = append(result.IDs, id)
		}
		return json.NewEncoder(output).Encode(result)
	}
	worker, err := valkeystream.NewWorkerE(options...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, worker.Shutdown()) }()
	if mode == "verify-ack" {
		stats, err := worker.Stats(ctx)
		if err != nil {
			return err
		}
		if !stats.LagKnown || stats.Depth != 0 || stats.Pending != 0 || stats.Lag != 0 {
			return errors.New("queue skew: acknowledged work remains pending or lagging")
		}
		return json.NewEncoder(output).Encode(result)
	}
	seen := make(map[string]bool)
	for len(result.IDs) < count {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, err := worker.Request()
		if errors.Is(err, queue.ErrNoTaskInQueue) && mode == "compete" && len(result.IDs) > 0 {
			break
		}
		if err != nil {
			return err
		}
		id, err := validateMessage(message, prefix)
		if err != nil {
			return err
		}
		if seen[id] {
			return errors.New("queue skew: duplicate task claim in one consumer")
		}
		seen[id] = true
		result.IDs = append(result.IDs, id)
		acknowledger, ok := message.(core.Acknowledger)
		if !ok || !acknowledger.AcknowledgementRequired() {
			return errors.New("queue skew: durable acknowledgement required")
		}
		if mode == "hold" || (mode == "compete" && len(result.IDs) == 1) {
			stats, err := worker.Stats(ctx)
			if err != nil {
				return err
			}
			result.Acknowledged, result.Reclaimed, result.Pending, result.Lag = stats.Acknowledged, stats.Reclaimed, stats.Pending, stats.Lag
			encoded, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if err := os.WriteFile(ready, encoded, 0o600); err != nil {
				return err
			}
			if mode == "hold" {
				<-ctx.Done()
				if parent.Err() != nil {
					return nil
				}
				return ctx.Err()
			}
			ticker := time.NewTicker(20 * time.Millisecond)
			for {
				_, err := os.Stat(release)
				if err == nil {
					break
				}
				if !errors.Is(err, os.ErrNotExist) {
					ticker.Stop()
					return err
				}
				select {
				case <-ctx.Done():
					ticker.Stop()
					return ctx.Err()
				case <-ticker.C:
				}
			}
			ticker.Stop()
		}
		if err := acknowledger.Ack(); err != nil {
			return err
		}
	}
	stats, err := worker.Stats(ctx)
	if err != nil {
		return err
	}
	result.Acknowledged, result.Reclaimed, result.Pending, result.Lag = stats.Acknowledged, stats.Reclaimed, stats.Pending, stats.Lag
	if result.Acknowledged != uint64(len(result.IDs)) || (reclaim && result.Reclaimed != uint64(len(result.IDs))) {
		return errors.New("queue skew: acknowledgement or reclaim outcome differs from delivered tasks")
	}
	return json.NewEncoder(output).Encode(result)
}
