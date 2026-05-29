package harness

import "github.com/aunali321/pi-go/llm"

func textOf(content []llm.Content) string {
	var s string
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok {
			s += t.Text
		}
	}
	return s
}
