# Account interactions

Likes and notification history are application services shared by content
writers. Each like belongs to an authenticated active account and one post or
comment. Repeating a request to set the same state does not add another like or
notification. Removing a like preserves the earlier notification as history;
liking the content again creates a new activity.

Comments address the selected reply target and its retained participants.
Optional participants can be excluded. The direct target cannot be excluded,
and explicitly mentioned accounts are resolved independently. Each recipient
receives one notification for an action, with all applicable reasons preserved.
The selected participants are stored with the comment so later replies do not
silently restore excluded people.

Mentions are extracted from Markdown prose and structured article text. Code,
links, email addresses, titles and media descriptions are not implicit mentions.
A handle keeps its resolved account binding across account renames. Unresolved
text is not rebound on unrelated edits. Removing a mention and adding it again
is a new action. Draft saves and archive reconstruction do not publish events.

Content mutations and their notifications commit in the same SQLite
transaction. A notification storage failure rolls back the content mutation,
including its retry record. Reading history does not mark it as viewed.

Each notification has a stable identifier and a separate commit sequence.
Pagination cursors are bound to the account, view and active data generation.
The All view and the Mentions view have separate monotonic viewing positions;
viewing mentions does not consume unviewed likes. Machine consumption maintains
its own progress.

Notifications store source references rather than body snapshots. Detail reads
return the latest authorized post, article or comment. Deleted, withdrawn or
inaccessible sources retain a minimal historical event without exposing a body
or preview, and do not contribute to the visible new-notification count.

Interaction tables are introduced by a database migration. Older executables
refuse to open a newer database. Switching back to an older executable is not a
data rollback; a rollback build must retain support for the migrated schema.
