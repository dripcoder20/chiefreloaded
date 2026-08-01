package session

import (
	"context"
	"fmt"
	"strconv"
)

// Questions are how the session asks for a decision without knowing what kind of
// interface is listening.
//
// chief asks with a modal dialog inside its Bubble Tea model, which welds the
// policy to the presentation: the branch-safety decision tree cannot be tested,
// scripted, or reused, because it only exists as keystroke handling. Here the
// session states the choice and someone else answers — a modal, a CLI prompt, a
// line in a test, or an auto-answer policy for headless runs.
//
// Nothing blocks while a question is outstanding. The asking goroutine waits on
// a channel; the rest of the session carries on, and other PRDs keep running.

// ask publishes a question and waits for the answer.
//
// It is called from a run's own goroutine, so blocking here stalls that run and
// nothing else — which is the intended behaviour, since the run genuinely cannot
// proceed until the decision is made.
func (s *Session) ask(ctx context.Context, q Question) (Answer, error) {
	s.mu.Lock()
	s.questionSeq++
	q.ID = QuestionID("q_" + strconv.Itoa(s.questionSeq))
	reply := make(chan Answer, 1)
	if s.pending == nil {
		s.pending = make(map[QuestionID]chan Answer)
	}
	s.pending[q.ID] = reply
	s.questions[q.ID] = q
	s.mu.Unlock()

	s.publish(Event{
		Kind: EvQuestionAsked, RunID: q.RunID, PRD: q.PRD,
		Text: q.Title, Question: &q,
	})

	select {
	case a := <-reply:
		return a, nil
	case <-ctx.Done():
		s.resolve(q.ID)
		return Answer{}, ctx.Err()
	}
}

// Answer resolves a pending question.
//
// An unknown ID is an error rather than a silent no-op: it almost always means
// the UI is answering a question from a previous run, and swallowing that would
// leave the real question hanging with no sign of why.
func (s *Session) Answer(id QuestionID, a Answer) error {
	s.mu.Lock()
	reply, ok := s.pending[id]
	q, known := s.questions[id]
	if ok {
		delete(s.pending, id)
		delete(s.questions, id)
	}
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("no pending question %q", id)
	}
	if a.OptionID == "" {
		a.OptionID = q.DefaultOption
	}
	if !validOption(q, a.OptionID) {
		// Put it back: rejecting the answer must not also lose the question.
		s.mu.Lock()
		s.pending[id] = reply
		s.questions[id] = q
		s.mu.Unlock()
		return fmt.Errorf("option %q is not one of the choices for %q", a.OptionID, q.Title)
	}

	reply <- a
	if known {
		s.publish(Event{
			Kind: EvQuestionResolved, RunID: q.RunID, PRD: q.PRD,
			Text: a.OptionID, Question: &q,
		})
	}
	return nil
}

// resolve drops a question without answering it, for shutdown and cancellation.
func (s *Session) resolve(id QuestionID) {
	s.mu.Lock()
	delete(s.pending, id)
	delete(s.questions, id)
	s.mu.Unlock()
}

func validOption(q Question, optionID string) bool {
	for _, o := range q.Options {
		if o.ID == optionID {
			return true
		}
	}
	return false
}

// AutoAnswer makes the session answer its own questions with the recommended
// option. It is how loopctl and the end-to-end tests run unattended.
//
// Not a default: a GUI silently choosing for the user would be far worse than
// waiting, particularly for the branch-safety question, where one of the options
// commits work directly to whatever branch they happen to be on.
func (s *Session) AutoAnswer(enabled bool) {
	s.mu.Lock()
	s.autoAnswer = enabled
	s.mu.Unlock()
}

// autoAnswering reports whether questions resolve themselves.
func (s *Session) autoAnswering() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoAnswer
}

// askOrDefault asks, unless auto-answering is on, in which case it takes the
// recommended option immediately.
func (s *Session) askOrDefault(ctx context.Context, q Question) (Answer, error) {
	if s.autoAnswering() {
		return Answer{OptionID: q.DefaultOption}, nil
	}
	return s.ask(ctx, q)
}
