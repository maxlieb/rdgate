package provision

import (
	"strings"
	"testing"
)

// schtasks rejects a task whose elements are out of schema order, and reports
// it only as "The task XML is malformed". These checks catch that without a
// Windows host.
func TestTaskXMLHasSchemaOrderAndEncoding(t *testing.T) {
	doc, err := BuildTaskXML("d", `C:\x\y.exe`, `web --config "C:\a b\app.ini"`, `C:\x`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatalf("declaration must match the UTF-16 file: %q", doc[:60])
	}
	order := []string{"<UserId>", "<LogonType>", "<RunLevel>"}
	last := -1
	for _, tag := range order {
		i := strings.Index(doc, tag)
		if i < 0 || i < last {
			t.Fatalf("%s is missing or out of schema order", tag)
		}
		last = i
	}
	if !strings.Contains(doc, "web --config &#34;C:\\a b\\app.ini&#34;") && !strings.Contains(doc, `web --config "C:\a b\app.ini"`) {
		t.Fatal("arguments were not written")
	}
}
