# Quickstart: validate 042

Prerequisites: a dev cluster (`make dev-up`, `make images && make dev-load`) with one
running and one stopped Minecraft server, one server with a game log file, and a
storage class that supports CSI snapshots plus a Restic destination. CI is the pass/fail
authority (CLAUDE.md rule 8); this walk is the human check (SC-007).

1. **Servers** (`/servers`, dark then light theme): five different card icon colours;
   force overcommit (storage larger than the system) and see an amber badge while the
   Storage icon stays teal; one frame around the table; full-contrast row actions with
   green Start / red Stop hover; search and Filter share 12 px corners; Tab reaches
   Filter and Enter opens it. Create server stays a pill.
2. **Status pill**: same pill size and dot on Dashboard, Servers, Server detail header,
   Backups and a network capture; put a server to sleep and check the Asleep colour.
3. **Logs** (`/servers/<name>` › Logs): source switch on one line; levels coloured
   Info blue, Debug slate, Warn amber, Error red, with count badges; line colours match
   the filter in both themes. Then check every other tab list at 1280 px: no wrapped
   label (SC-003).
4. **Backups**: on a server with ~14 GB of data on a 200 GiB volume, take one Restic and
   one CSI backup. Size shows ~14 GB for both (SC-004); Restic shows a second "added"
   line, CSI none; the card reads "Total data size". An older backup follows OD-8.
   Stop the agent and take a CSI backup: Size shows unknown, never 200 GiB.
5. **Resources** (Create server step 3, Settings › Resources): every slider filled up
   to the handle; 8000 mCPU fully visible; labels without units; Storage size is number
   + unit; a server created with `1536Mi` keeps `1536Mi` after saving unchanged.
   SFTP password dialog slider has the same look.
