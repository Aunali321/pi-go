package llm

// Content is a message content block: *Text, *Thinking, *Image or *ToolCall.
type Content interface {
	isContent()
}

type Text struct {
	Text      string
	Signature string
}

func (*Text) isContent() {}

type Thinking struct {
	Thinking  string
	Signature string
	Redacted  bool
}

func (*Thinking) isContent() {}

type Image struct {
	Data     string
	MimeType string
}

func (*Image) isContent() {}

type ToolCall struct {
	ID               string
	Name             string
	Arguments        map[string]any
	ThoughtSignature string

	partialArgs string
	streamIndex int
	hasIndex    bool
}

func (*ToolCall) isContent() {}

func textBlocks(content []Content) []*Text {
	var out []*Text
	for _, c := range content {
		if t, ok := c.(*Text); ok {
			out = append(out, t)
		}
	}
	return out
}

func toolCalls(content []Content) []*ToolCall {
	var out []*ToolCall
	for _, c := range content {
		if tc, ok := c.(*ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}
