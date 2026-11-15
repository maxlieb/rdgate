package provision

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// Task names, also what the user sees in Task Scheduler.
const (
	TaskGateway = "RDGateway-Gateway"
	TaskSignIn  = "RDGateway-SignIn"
	TaskCaddy   = "RDGateway-Web"
	TaskGate    = "RDGateway-Gate"
	TaskDDNS    = "RDGateway-Address"
)

// Neither rdpgw nor Gitea is a service-aware binary, so sc.exe would start
// them and then kill them for failing to report back. A real service needs a
// wrapper like WinSW or NSSM - a dependency worth avoiding while there is a
// workable alternative.
//
// Scheduled tasks are that alternative, and registering them from XML rather
// than schtasks flags buys two things the flags cannot express: a working
// directory, and restart-on-failure.

type taskXML struct {
	XMLName   xml.Name `xml:"Task"`
	Version   string   `xml:"version,attr"`
	Namespace string   `xml:"xmlns,attr"`

	RegistrationInfo struct {
		Description string `xml:"Description"`
		Author      string `xml:"Author"`
	} `xml:"RegistrationInfo"`

	Triggers struct {
		BootTrigger struct {
			Enabled bool   `xml:"Enabled"`
			Delay   string `xml:"Delay,omitempty"`
		} `xml:"BootTrigger"`
	} `xml:"Triggers"`

	Principals struct {
		Principal struct {
			ID       string `xml:"id,attr"`
			UserID   string `xml:"UserId"`
			RunLevel string `xml:"RunLevel"`
		} `xml:"Principal"`
	} `xml:"Principals"`

	Settings struct {
		MultipleInstancesPolicy    string `xml:"MultipleInstancesPolicy"`
		DisallowStartIfOnBatteries bool   `xml:"DisallowStartIfOnBatteries"`
		StopIfGoingOnBatteries     bool   `xml:"StopIfGoingOnBatteries"`
		AllowHardTerminate         bool   `xml:"AllowHardTerminate"`
		StartWhenAvailable         bool   `xml:"StartWhenAvailable"`
		RunOnlyIfNetworkAvailable  bool   `xml:"RunOnlyIfNetworkAvailable"`
		Enabled                    bool   `xml:"Enabled"`
		Hidden                     bool   `xml:"Hidden"`
		ExecutionTimeLimit         string `xml:"ExecutionTimeLimit"`
		Priority                   int    `xml:"Priority"`
		RestartOnFailure           struct {
			Interval string `xml:"Interval"`
			Count    int    `xml:"Count"`
		} `xml:"RestartOnFailure"`
	} `xml:"Settings"`

	Actions struct {
		Context string `xml:"Context,attr"`
		Exec    struct {
			Command          string `xml:"Command"`
			Arguments        string `xml:"Arguments"`
			WorkingDirectory string `xml:"WorkingDirectory"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

const taskXMLHeader = `<?xml version="1.0" encoding="UTF-16"?>` + "\n"

// BuildTaskXML renders the task definition. Exported so it can be tested
// without a Windows host, and so the UI can show it if anyone asks what
// exactly is being registered.
func BuildTaskXML(description, command, arguments, workingDir string) (string, error) {
	var t taskXML
	t.Version = "1.4"
	t.Namespace = "http://schemas.microsoft.com/windows/2004/02/mit/task"
	t.RegistrationInfo.Description = description
	t.RegistrationInfo.Author = "RDGate"

	t.Triggers.BootTrigger.Enabled = true
	// Let the network stack and the Tailscale service settle before the
	// gateway tries to bind and Funnel tries to publish.
	t.Triggers.BootTrigger.Delay = "PT30S"

	t.Principals.Principal.ID = "Author"
	t.Principals.Principal.UserID = "S-1-5-18" // SYSTEM, by SID so it is locale independent
	t.Principals.Principal.RunLevel = "HighestAvailable"
	// LogonType is left out on purpose: "ServiceAccount" is not a value in the
	// schema version used here, and Task Scheduler rejects the whole task for it.
	// SYSTEM by SID with RunLevel is enough to run the task as SYSTEM.

	t.Settings.MultipleInstancesPolicy = "IgnoreNew"
	t.Settings.DisallowStartIfOnBatteries = false
	t.Settings.StopIfGoingOnBatteries = false
	t.Settings.AllowHardTerminate = true
	t.Settings.StartWhenAvailable = true
	t.Settings.RunOnlyIfNetworkAvailable = false
	t.Settings.Enabled = true
	t.Settings.Hidden = false
	t.Settings.ExecutionTimeLimit = "PT0S" // no limit; these run forever
	t.Settings.Priority = 5
	t.Settings.RestartOnFailure.Interval = "PT1M"
	t.Settings.RestartOnFailure.Count = 3

	t.Actions.Context = "Author"
	t.Actions.Exec.Command = command
	t.Actions.Exec.Arguments = arguments
	t.Actions.Exec.WorkingDirectory = workingDir

	body, err := xml.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", err
	}
	// The file is written as UTF-16LE (see RegisterTask), so the declaration
	// must say so; a UTF-8 declaration on UTF-16 bytes is malformed to schtasks.
	return taskXMLHeader + string(body), nil
}

// RegisterTask creates or replaces a startup task.
func RegisterTask(ctx context.Context, name, description, command, arguments, workingDir string) error {
	doc, err := BuildTaskXML(description, command, arguments, workingDir)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "rdgw-task-*.xml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	// schtasks /XML is reliable only with UTF-16LE input; UTF-8 is rejected
	// on some builds with an unhelpful "The task XML is malformed".
	if _, err := f.Write(utf16LEWithBOM(doc)); err != nil {
		f.Close()
		return err
	}
	f.Close()

	out, err := Command(ctx, "schtasks", "/Create", "/TN", name, "/XML", tmp, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("registering task %s: %s", name, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}

// utf16LEWithBOM encodes ASCII-safe XML as UTF-16LE. The documents produced
// here contain only characters from the file paths and fixed strings, so a
// surrogate-free conversion is sufficient.
func utf16LEWithBOM(s string) []byte {
	out := make([]byte, 0, len(s)*2+2)
	out = append(out, 0xFF, 0xFE)
	for _, r := range s {
		if r > 0xFFFF {
			r = '?'
		}
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func StartTask(ctx context.Context, name string) error {
	out, err := Command(ctx, "schtasks", "/Run", "/TN", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting task %s: %s", name, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}

// StopTask is best effort: a task that is not running is not an error worth
// surfacing during teardown.
func StopTask(ctx context.Context, name string) {
	_ = Command(ctx, "schtasks", "/End", "/TN", name).Run()
}

func RemoveTask(ctx context.Context, name string) error {
	out, err := Command(ctx, "schtasks", "/Delete", "/TN", name, "/F").CombinedOutput()
	if err != nil {
		s := strings.ToLower(string(out))
		// Already gone is a success for our purposes.
		if strings.Contains(s, "cannot find") || strings.Contains(s, "does not exist") {
			return nil
		}
		return fmt.Errorf("removing task %s: %s", name, firstLine(strings.TrimSpace(string(out))))
	}
	return nil
}

func TaskExists(ctx context.Context, name string) bool {
	return Command(ctx, "schtasks", "/Query", "/TN", name).Run() == nil
}
