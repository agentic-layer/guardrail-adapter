package sse

import "bytes"

// Encode renders a canonical SSE event from name, id, and data. name
// equal to "" or "message" is treated as the default and the "event:"
// line is omitted. id equal to "" causes the "id:" line to be omitted.
// data is written verbatim on a single "data:" line; callers must
// ensure data does not contain a literal newline (minified JSON
// satisfies this for the adapter's use cases).
func Encode(name, id string, data []byte) []byte {
	var b bytes.Buffer
	if name != "" && name != "message" {
		b.WriteString("event: ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if id != "" {
		b.WriteString("id: ")
		b.WriteString(id)
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	return b.Bytes()
}
