package cli

import (
	"reflect"
	"testing"
)

func TestCommentContextAndDirectRepliesTransportParity(t *testing.T) {
	fixture := newCommentCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	create := func(args ...string) map[string]any {
		t.Helper()
		flags := append([]string{"--url", fixture.server.URL, "comment"}, args...)
		flags = append(flags, "--json")
		return commandData(t, execute(application, "", flags...))
	}
	root := create("create", fixture.postID, "--content", "root")["id"].(string)
	target := create("reply", fixture.postID, root, "--reply-to", root, "--content", "target")["id"].(string)
	child := create("reply", fixture.postID, root, "--reply-to", target, "--content", "child")["id"].(string)
	for _, args := range [][]string{
		{"comment", "get", fixture.postID, child},
		{"comment", "get", fixture.postID, root},
		{"comment", "replies", fixture.postID, target, "--scope", "direct"},
		{"comment", "replies", fixture.postID, root},
	} {
		apiOutput := execute(application, "", append(append([]string{"--url", fixture.server.URL}, args...), "--json")...)
		directOutput := execute(application, "", append(append([]string{"--data-dir", fixture.dataDir, "--as", "writer"}, args...), "--json")...)
		if apiOutput.exit != ExitSuccess || directOutput.exit != ExitSuccess || apiOutput.stderr != "" || directOutput.stderr != "" {
			t.Fatalf("args=%v api=%#v direct=%#v", args, apiOutput, directOutput)
		}
		api, direct := commandData(t, apiOutput), commandData(t, directOutput)
		if !reflect.DeepEqual(api, direct) {
			t.Fatalf("args=%v api=%#v direct=%#v", args, api, direct)
		}
	}
	context := commandData(t, execute(application, "", "--url", fixture.server.URL, "comment", "get", fixture.postID, child, "--json"))
	if context["root_comment_id"] != root || context["reply_target"].(map[string]any)["id"] != target || context["direct_reply_count"] != float64(0) {
		t.Fatalf("context=%#v", context)
	}
	replies := commandData(t, execute(application, "", "--url", fixture.server.URL, "comment", "replies", fixture.postID, target, "--scope", "direct", "--json"))
	if items := replies["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != child {
		t.Fatalf("direct replies=%#v", replies)
	}
	for _, mode := range [][]string{{"--url", fixture.server.URL}, {"--data-dir", fixture.dataDir}} {
		out := execute(application, "", append(mode, "comment", "replies", fixture.postID, target, "--scope", "wrong", "--json")...)
		if out.exit != ExitInvalidInput {
			t.Fatalf("invalid scope=%#v", out)
		}
		missing := execute(application, "", append(mode, "comment", "get", fixture.postID, "01ARZ3NDEKTSV4RRFFQ69G5FA0", "--json")...)
		if missing.exit != ExitNotFound {
			t.Fatalf("missing=%#v", missing)
		}
	}
}
