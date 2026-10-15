package main

import (
	"runtime/debug"
	"testing"
	"time"

	"github.com/faustbrian/go-queue/job"
)

func TestExactQueueIdentityRejectsMVSDriftAndReplacements(t *testing.T) {
	t.Parallel()
	for name, dependency := range map[string]*debug.Module{
		"exact":         {Path: "github.com/faustbrian/go-queue", Version: "v1.1.0"},
		"raised by MVS": {Path: "github.com/faustbrian/go-queue", Version: "v1.1.2"},
		"replacement":   {Path: "github.com/faustbrian/go-queue", Version: "v1.1.0", Replace: &debug.Module{Path: "../queue"}},
		"missing":       {Path: "other.test/module", Version: "v1.1.0"},
	} {
		t.Run(name, func(t *testing.T) {
			err := verifyQueueVersion(&debug.BuildInfo{Deps: []*debug.Module{dependency}}, "v1.1.0")
			if (err == nil) != (name == "exact") {
				t.Fatalf("identity %s accepted=%t", name, err == nil)
			}
		})
	}
}

func TestCommonFieldsValidationRejectsPayloadOrExecutionDrift(t *testing.T) {
	t.Parallel()
	good := commonMessage("upgrade-0")
	// The probe intentionally publishes only these common, explicit fields.
	if id, err := validateMessage(good, "upgrade"); err != nil || id != "upgrade-0" {
		t.Fatalf("common message rejected: %s, %v", id, err)
	}
	for name, change := range map[string]func(*job.Message){
		"wrong cohort": func(message *job.Message) { message.Body = []byte(`{"id":"other-0","value":"queue-common-v1"}`) },
		"unknown field": func(message *job.Message) {
			message.Body = []byte(`{"id":"upgrade-0","value":"queue-common-v1","future":true}`)
		},
		"wrong value":   func(message *job.Message) { message.Body = []byte(`{"id":"upgrade-0","value":"changed"}`) },
		"timeout drift": func(message *job.Message) { message.Timeout = time.Second },
		"retry drift":   func(message *job.Message) { message.RetryCount = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *good
			change(&changed)
			if _, err := validateMessage(&changed, "upgrade"); err == nil {
				t.Fatal("changed common fields accepted")
			}
		})
	}
}
