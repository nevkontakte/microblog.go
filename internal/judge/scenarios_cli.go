package judge

import (
	"os"
	"path/filepath"
	"strings"
)

// CLIScenarios cover what federates without an HTTP request from the
// admin: the original's command-line tasks, and a profile change picked up
// at start-up. Both need the instance to run commands or restart.
var CLIScenarios = []Scenario{
	// The outgoing worker sends an Update of the local actor when the profile
	// changed since its last start (app/outgoing_activities.py:38-97).
	{Name: "actor_update__profile_change", Run: func(t *T) {
		follow(t, t.B, t.A)
		t.restart(t.A, `summary = "Updated"`) // Markdown, rendered into the actor
		t.waitRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Update'`)
		t.waitRows(t.B, 1, `SELECT 1 FROM actor WHERE ap_id = ?
			AND json_extract(ap_actor, '$.summary') = ?`, BaseURL(t.A), "<p>Updated</p>\n")
	}},

	// `inv move-to` (tasks.py:260-309): a's followers move to the new account.
	// The new actor is shown in a's profile after a restart, which also sends
	// the profile Update.
	{Name: "move__to_other_account", Run: func(t *T) {
		moved := t.NewFake("new")
		moved.AlsoKnownAs = []string{BaseURL(t.A)}
		moved.AutoAccept = true
		follow(t, t.B, t.A)
		out, err := t.A.Command(t.Ctx, "", "move-to", "@new@new.test")
		t.Check(err)
		if !strings.Contains(out, "Done") {
			t.Fatalf("move-to: %s", out)
		}
		move := t.one(t.A, `SELECT ap_id, json_extract(ap_object, '$.target') AS target FROM outbox WHERE ap_type = 'Move'`)
		t.equal("Move target", move["target"], moved.ID())
		// b unfollows a, follows the new account, and is notified
		// (app/boxes.py:1693-1756).
		t.waitProcessed(t.B, move["ap_id"])
		t.waitRows(t.B, 0, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(t.A))
		t.waitRows(t.B, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, moved.ID())
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'MOVE'`)
		t.waitRows(t.A, 0, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(t.B))

		t.restart(t.A, "")
		actor, err := t.Client.GetAP(t.Ctx, BaseURL(t.A))
		t.Check(err)
		t.equal("movedTo", actor["movedTo"], moved.ID())
		update := t.waitRow(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Update'`)
		t.waitProcessed(t.B, update["ap_id"])
	}},

	// `inv self-destruct` (tasks.py:312-331) deletes the local actor
	// everywhere: b drops a as a follower and as followed, and marks its
	// Follow of a deleted (app/boxes.py:1170-1230).
	{Name: "self_destruct", Run: func(t *T) {
		follow(t, t.B, t.A)
		follow(t, t.A, t.B)
		out, err := t.A.Command(t.Ctx, "yes\n", "self-destruct")
		t.Check(err)
		if !strings.Contains(out, "Done") {
			t.Fatalf("self-destruct: %s", out)
		}
		t.equal("Delete", t.one(t.A, `SELECT activity_object_ap_id FROM outbox WHERE ap_type = 'Delete'`)["activity_object_ap_id"], BaseURL(t.A))
		t.waitRows(t.B, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_deleted = 1`, BaseURL(t.A))
		t.waitRows(t.B, 0, `SELECT 1 FROM follower`)
		t.waitRows(t.B, 0, `SELECT 1 FROM following`)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Follow' AND activity_object_ap_id = ? AND is_deleted = 1`, BaseURL(t.A))
	}},

	// `inv import-mastodon-following-accounts` (tasks.py:389-424) follows
	// every account in a Mastodon export.
	{Name: "import_following", Run: func(t *T) {
		csv := filepath.Join(t.A.DataDir(), "following_accounts.csv")
		t.Check(os.WriteFile(csv, []byte("Account address,Show boosts,Notify on new posts,Languages\nb@b.test,true,false,\n"), 0o644))
		_, err := t.A.Command(t.Ctx, "", "import-mastodon-following-accounts", csv)
		t.Check(err)
		t.sawExchange("a", "GET", finger(t.B, "b"), 200)
		t.waitRows(t.A, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(t.B))
		t.waitRows(t.B, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(t.A))
	}},
}

// restart lets the pair settle, stops i, and starts it again on its data
// with config added to its profile.toml lines (a key set again overrides
// the earlier value), as an admin editing the profile would. It returns the
// new instance, which also replaces i in t. Until the new instance's address
// is routed, its host still points at the stopped one; nothing is pending by
// then. The admin session survives: its cookie is signed with the same
// secret.
func (t *T) restart(i Instance, config string) Instance {
	t.settle(0)
	t.Check(i.Stop())
	spec := t.specs[i.Name()]
	spec.Config += "\n" + config
	t.specs[i.Name()] = spec
	inst, err := t.start(i.Name())
	t.Check(err)
	t.Check(t.waitReady(inst))
	if i == t.A {
		t.A = inst
	} else {
		t.B = inst
	}
	return inst
}
