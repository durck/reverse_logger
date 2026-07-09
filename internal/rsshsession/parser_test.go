package rsshsession

import "testing"

func TestParseListOutputParsesReverseSSHLine(t *testing.T) {
	output := `catcher$ ls
f8f1f59eab1730bb286de114a6981454e670d432 3ba18e89b52184a93821c51856c71e0a5e303fc1 tn.mamontovdk.zcr-img002-0633 92.246.76.17:60266, owners: public, version: SSH-windows_amd64
catcher$ exit
`
	sessions, err := ParseListOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d", len(sessions))
	}
	session := sessions[0]
	if session.ReverseSSHID != "f8f1f59eab1730bb286de114a6981454e670d432" {
		t.Fatalf("reverse ssh id = %q", session.ReverseSSHID)
	}
	if session.HostName != "tn.mamontovdk.zcr-img002-0633" {
		t.Fatalf("host name = %q", session.HostName)
	}
	if session.RemoteAddr != "92.246.76.17:60266" || session.Owners != "public" || session.Version != "SSH-windows_amd64" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestParseListOutputIgnoresConsoleNoiseAndEmptyListing(t *testing.T) {
	output := "\x1b[33mcatcher$ \x1b[0mls\r\nUnknown command: вы\r\nNo RSSH clients connected\r\ncatcher$ exit\r\n"
	sessions, err := ParseListOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestParseListOutputRejectsEmptyOrPromptOnlyOutput(t *testing.T) {
	for _, output := range []string{
		"",
		"catcher$ ls\ncatcher$ exit\n",
	} {
		if _, err := ParseListOutput(output); err == nil {
			t.Fatalf("expected parse error for output %q", output)
		}
	}
}

func TestParseListOutputRejectsUnclassifiedLines(t *testing.T) {
	_, err := ParseListOutput("this is not a reverse_ssh listing\n")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseListOutputRejectsConcatenatedConsoleCommands(t *testing.T) {
	_, err := ParseListOutput("catcher$ lsexit\n")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLiveSessionIDsDedupesAndDropsEmptyIDs(t *testing.T) {
	ids := LiveSessionIDs([]LiveSession{
		{ReverseSSHID: " one "},
		{ReverseSSHID: "one"},
		{ReverseSSHID: ""},
		{ReverseSSHID: "two"},
	})
	if len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
		t.Fatalf("ids = %#v", ids)
	}
}
