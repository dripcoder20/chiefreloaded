package session

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/dripcoder/loop/internal/authoring"
	"github.com/dripcoder/loop/internal/prompts"
)

// PRD authoring, exposed through the session so the Wails layer and loopctl
// reach it the same way as everything else.
//
// Terminal output does not go on the main event stream. It is high-volume,
// binary, and interesting to exactly one component; putting it on the ordered
// firehose would push real events out of the retention ring for no benefit. It
// gets its own callback and its own Wails event instead.

// AuthorEvent is a chunk of terminal output from an authoring session.
type AuthorEvent struct {
	SessionID string `json:"sessionId"`
	// Base64 because terminal output is bytes, not text: partial UTF-8 sequences
	// split across chunk boundaries would be mangled by JSON string encoding.
	Data string `json:"data"`
}

// AuthorExit reports that a session finished and what it produced.
type AuthorExit struct {
	SessionID string            `json:"sessionId"`
	Outcome   authoring.Outcome `json:"outcome"`
	Spec      authoring.Spec    `json:"spec"`
}

// SetAuthoringSinks installs the callbacks that carry terminal output and
// completion to the front-end. Called once at startup by the Wails bridge.
func (s *Session) SetAuthoringSinks(onData func(AuthorEvent), onExit func(AuthorExit)) {
	s.authoring.OnData = func(id string, chunk []byte) {
		if onData != nil {
			onData(AuthorEvent{SessionID: id, Data: base64.StdEncoding.EncodeToString(chunk)})
		}
	}
	s.authoring.OnExit = func(id string, o authoring.Outcome) {
		s.mu.Lock()
		spec := s.authorSpecs[id]
		delete(s.authorSpecs, id)
		s.mu.Unlock()

		// The PRD list is stale the moment a session writes one, and the user is
		// looking straight at it.
		_ = s.Rescan(context.Background())

		if onExit != nil {
			onExit(AuthorExit{SessionID: id, Outcome: o, Spec: spec})
		}
	}
}

// StartAuthoring begins an interactive agent session to write or revise a PRD.
func (s *Session) StartAuthoring(spec authoring.Spec) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}

	// The authoring phase has its own configured agent: the best agent for a long
	// interactive conversation is not necessarily the best one for an unattended
	// run. spec.Agent overrides it for this session only.
	agentName := spec.Agent
	if agentName == "" {
		agentName = s.DefaultAuthoringAgent()
	}
	provider, err := s.resolveProvider(agentName)
	if err != nil {
		return "", err
	}

	id, err := s.authoring.Start(root, provider, spec)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.authorSpecs[id] = spec
	s.mu.Unlock()
	return id, nil
}

// WriteAuthoring sends keystrokes to a session. data is base64.
func (s *Session) WriteAuthoring(id, data string) error {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	return s.authoring.Write(id, raw)
}

// ResizeAuthoring tells a session its terminal changed size.
func (s *Session) ResizeAuthoring(id string, cols, rows int) error {
	return s.authoring.Resize(id, cols, rows)
}

// InterruptAuthoring sends Ctrl-C.
func (s *Session) InterruptAuthoring(id string) error { return s.authoring.Interrupt(id) }

// StopAuthoring ends a session and everything it spawned.
func (s *Session) StopAuthoring(id string) error { return s.authoring.Stop(id) }

// AuthoringScrollback returns a session's output so far, base64 encoded, so a
// view that was closed and reopened can redraw rather than showing a blank
// terminal mid-conversation.
func (s *Session) AuthoringScrollback(id string) (string, error) {
	raw, err := s.authoring.Scrollback(id)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// ------------------------------------------------------------------ prompts --

// Prompt returns the project's authoring prompt for kind, which is chief's
// built-in unless the project has overridden it.
func (s *Session) Prompt(kind string) (prompts.Prompt, error) {
	root, err := s.requireProject()
	if err != nil {
		return prompts.Prompt{}, err
	}
	return prompts.Load(root, prompts.Kind(kind))
}

// SavePrompt writes a project override. An empty body, or one identical to the
// built-in, clears the override instead.
func (s *Session) SavePrompt(kind, body string) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}
	if err := prompts.Save(root, prompts.Kind(kind), body); err != nil {
		return err
	}
	s.publish(Event{Kind: EvConfigChanged, Text: "prompt " + kind})
	return nil
}

// ResetPrompt removes a project override.
func (s *Session) ResetPrompt(kind string) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}
	if err := prompts.Reset(root, prompts.Kind(kind)); err != nil {
		return err
	}
	s.publish(Event{Kind: EvConfigChanged, Text: "prompt " + kind})
	return nil
}

// BuiltinPrompt returns chief's own prompt for kind, so the editor can offer a
// starting point and a diff against the default.
func (s *Session) BuiltinPrompt(kind string) string {
	return prompts.Builtin(prompts.Kind(kind))
}
