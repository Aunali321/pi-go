package llm

import "sync"

// Stream carries the events of a streaming request. The Events channel closes
// when the stream terminates; Result blocks until the final message is ready.
type Stream struct {
	ch chan StreamEvent

	mu        sync.Mutex
	done      chan struct{}
	result    *AssistantMessage
	finished  bool
	closeOnce sync.Once
}

func newStream() *Stream {
	return &Stream{
		ch:   make(chan StreamEvent, 64),
		done: make(chan struct{}),
	}
}

func (s *Stream) Events() <-chan StreamEvent { return s.ch }

func (s *Stream) Result() *AssistantMessage {
	<-s.done
	return s.result
}

func (s *Stream) push(ev StreamEvent) {
	switch e := ev.(type) {
	case DoneEvent:
		s.setResult(e.Message)
	case ErrorEvent:
		s.setResult(e.Error)
	}
	s.ch <- ev
}

func (s *Stream) setResult(m *AssistantMessage) {
	s.mu.Lock()
	if !s.finished {
		s.finished = true
		s.result = m
		close(s.done)
	}
	s.mu.Unlock()
}

func (s *Stream) close(m *AssistantMessage) {
	s.setResult(m)
	s.closeOnce.Do(func() { close(s.ch) })
}

// StreamFromMessage adapts a finished assistant message into a Stream. Useful
// for custom providers and tests.
func StreamFromMessage(m *AssistantMessage) *Stream {
	s := newStream()
	go func() {
		s.push(StartEvent{baseEvent{m}})
		if m.StopReason == StopError || m.StopReason == StopAborted {
			s.push(ErrorEvent{baseEvent{m}, m.StopReason, m})
		} else {
			s.push(DoneEvent{baseEvent{m}, m.StopReason, m})
		}
		s.close(m)
	}()
	return s
}
