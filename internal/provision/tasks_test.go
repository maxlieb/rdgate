package provision

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestBuildTaskXMLIsWellFormed(t *testing.T) {
	doc, err := BuildTaskXML("RD Gateway", `C:\x\rdpgw.exe`, `--conf "C:\x\rdpgw.yaml"`, `C:\x`)
	if err != nil {
		t.Fatalf("BuildTaskXML: %v", err)
	}
	var probe any
	if err := xml.Unmarshal([]byte(doc), &probe); err != nil {
		t.Fatalf("generated task XML does not parse: %v\n%s", err, doc)
	}
}

func TestBuildTaskXMLCarriesTheSettingsFlagsCannotExpress(t *testing.T) {
	// Working directory and restart-on-failure are the whole reason this is
	// XML rather than a handful of schtasks flags. If they vanish, the task
	// still registers and the reason for the complexity is silently lost.
	doc, err := BuildTaskXML("RD Gateway", `C:\x\rdpgw.exe`, "--conf x", `C:\x`)
	if err != nil {
		t.Fatalf("BuildTaskXML: %v", err)
	}
	for _, want := range []string{
		"<WorkingDirectory>C:\\x</WorkingDirectory>",
		"<RestartOnFailure>",
		"<Count>3</Count>",
		"<UserId>S-1-5-18</UserId>", // SYSTEM by SID, so locale cannot break it
		"<RunLevel>HighestAvailable</RunLevel>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("task XML missing %q\n---\n%s", want, doc)
		}
	}
}

func TestBuildTaskXMLEscapesArguments(t *testing.T) {
	// Arguments contain quotes around paths; unescaped they would corrupt
	// the document and schtasks reports only "the task XML is malformed".
	doc, err := BuildTaskXML("d", `C:\x.exe`, `--conf "C:\Program Files\a & b\c.yaml"`, `C:\x`)
	if err != nil {
		t.Fatalf("BuildTaskXML: %v", err)
	}
	if strings.Contains(doc, "& b") {
		t.Error("a bare ampersand reached the document; it must be escaped")
	}
	var probe any
	if err := xml.Unmarshal([]byte(doc), &probe); err != nil {
		t.Fatalf("document with quoted arguments does not parse: %v", err)
	}
}

func TestUTF16LEHasBOM(t *testing.T) {
	// schtasks /XML rejects UTF-8 on some builds with an unhelpful message,
	// so the encoding is load-bearing rather than cosmetic.
	b := utf16LEWithBOM("<a/>")
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatalf("missing UTF-16LE BOM, got % x", b[:min(4, len(b))])
	}
	if b[2] != '<' || b[3] != 0x00 {
		t.Errorf("expected little-endian encoding, got % x", b[2:6])
	}
}

func TestGiteaURLs(t *testing.T) {
	g := NewGitea(Config{InstallRoot: `C:\ProgramData\RDGateway`, GiteaPort: 3000})
	const v = "1.24.3"
	wantDL := "https://dl.gitea.com/gitea/1.24.3/gitea-1.24.3-windows-4.0-amd64.exe"
	if got := g.DownloadURL(v); got != wantDL {
		t.Errorf("DownloadURL = %q, want %q", got, wantDL)
	}
	if got := g.ChecksumURL(v); got != wantDL+".sha256" {
		t.Errorf("ChecksumURL = %q", got)
	}
}

func TestNewGiteaPaths(t *testing.T) {
	g := NewGitea(Config{InstallRoot: `C:\ProgramData\RDGateway`})
	if g.Exe != `C:\ProgramData\RDGateway\gitea\gitea.exe` {
		t.Errorf("Exe = %q", g.Exe)
	}
	if g.AppIni != `C:\ProgramData\RDGateway\gitea\custom\conf\app.ini` {
		t.Errorf("AppIni = %q", g.AppIni)
	}
}

func TestTwoFactorURL(t *testing.T) {
	c := Config{Hostname: "pc.tailnet.ts.net", FrontDoor: FrontDoorFunnel}
	want := "https://pc.tailnet.ts.net/auth/user/settings/security/two_factor"
	if got := c.TwoFactorURL(); got != want {
		t.Errorf("TwoFactorURL = %q, want %q", got, want)
	}
}

// Uninstall must never be able to turn a lost install path into RemoveAll("").
func TestUninstallRefusesEmptyInstallRoot(t *testing.T) {
	errs := Uninstall(t.Context(), Config{InstallRoot: ""},
		UninstallOptions{KeepData: false, ResetFunnel: false}, nil)
	if len(errs) == 0 {
		t.Fatal("expected an error when the install root is empty")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "install path is empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the empty-path guard to fire, got %v", errs)
	}
}
