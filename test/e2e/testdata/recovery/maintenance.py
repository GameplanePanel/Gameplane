"""Offline SQLite maintenance for the isolated recovery fixture, never production.

The API must be stopped for backup/loss/restore. No key is mounted here. Output
contains only counts, timestamps and digests, never rows, credentials or tokens.
"""
import datetime
from contextlib import closing
import hashlib
import json
import pathlib
import secrets
import sqlite3
import sys
import time
import urllib.error
import urllib.request

DATA = pathlib.Path("/data/gameplane.db")
ARCHIVE = pathlib.Path("/archive/panel-backup.db")


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def readonly(path):
    return sqlite3.connect("file:" + str(path) + "?mode=ro", uri=True)


def inventory(db):
    assert db.execute("PRAGMA integrity_check").fetchall() == [("ok",)]
    rows = db.execute("SELECT * FROM audit_events ORDER BY id").fetchall()
    return {
        "audit_rows": len(rows),
        "audit_digest": hashlib.sha256(json.dumps(rows, separators=(",", ":")).encode()).hexdigest(),
        "users": db.execute("SELECT COUNT(*) FROM users").fetchone()[0],
        "sessions": db.execute("SELECT COUNT(*) FROM sessions").fetchone()[0],
        "api_tokens": db.execute("SELECT COUNT(*) FROM api_tokens").fetchone()[0],
        "active_shares": db.execute("SELECT COUNT(*) FROM share_links WHERE revoked_at IS NULL").fetchone()[0],
    }


def remove_working_database():
    for suffix in ("", "-wal", "-shm"):
        pathlib.Path(str(DATA) + suffix).unlink(missing_ok=True)


op = sys.argv[1]
if op == "seed-api-token":
    # The API currently authenticates only session cookies; api_tokens is a
    # reserved table with no issuance/Bearer-auth route. Seed its cleanup
    # coverage offline, after the API stops, without exporting the token.
    assert DATA.is_file() and not ARCHIVE.exists(), "seed before backup"
    with closing(sqlite3.connect(DATA)) as db:
        user = db.execute("SELECT id FROM users WHERE username=?", ("admin",)).fetchone()
        assert user is not None, "fixture administrator missing"
        db.execute("INSERT INTO api_tokens(token, user_id, name) VALUES (?, ?, ?)",
                   (secrets.token_urlsafe(32), user[0], "recovery-fixture"))
        db.commit()
        result = {"api_tokens": db.execute("SELECT COUNT(*) FROM api_tokens").fetchone()[0]}
elif op == "backup":
    assert not ARCHIVE.exists(), "archive must not be overwritten"
    with closing(readonly(DATA)) as source, closing(sqlite3.connect(ARCHIVE)) as dest:
        source.backup(dest)
        # Make the archive self-contained; do not retain a destination WAL.
        dest.execute("PRAGMA journal_mode=DELETE")
        result = inventory(dest)
    # The original archive is never modified again, including by auth cleanup.
    ARCHIVE.chmod(0o400)
    result.update(archive_digest=digest(ARCHIVE), backup_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
elif op == "loss":
    assert ARCHIVE.is_file(), "loss requires a separate archive"
    remove_working_database()
    result = {"working_database_absent": not DATA.exists(), "archive_digest": digest(ARCHIVE)}
elif op == "restore":
    assert not DATA.exists(), "restore only into the deleted working database"
    before = digest(ARCHIVE)
    with closing(readonly(ARCHIVE)) as source, closing(sqlite3.connect(DATA)) as dest:
        source.backup(dest)
        original = inventory(source)
        assert inventory(dest) == original
        # Invalidate restored bearer credentials on the working copy, preserving
        # the original archive and audit chain for incident investigation.
        dest.execute("DELETE FROM sessions")
        dest.execute("DELETE FROM api_tokens")
        dest.execute("UPDATE share_links SET revoked_at=? WHERE revoked_at IS NULL", (datetime.datetime.now(datetime.timezone.utc).isoformat(),))
        dest.commit()
        result = inventory(dest)
        assert result["audit_digest"] == original["audit_digest"]
        assert result["sessions"] == 0 and result["api_tokens"] == 0 and result["active_shares"] == 0
    DATA.chmod(0o600)
    assert digest(ARCHIVE) == before
    result["archive_digest"] = before
elif op == "archive":
    result = {"archive_digest": digest(ARCHIVE)}
elif op == "http-check":
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(sys.argv[2], timeout=2) as response:
            result = {"status": response.status}
    except (urllib.error.URLError, TimeoutError, OSError):
        result = {"status": 0}
elif op == "probe":
    stop = pathlib.Path("/data/probe-stop")
    stop.unlink(missing_ok=True)
    pathlib.Path("/data/probe-ready").write_text("ready")
    attempts, responses = 0, 0
    deadline = time.monotonic() + 120
    # Bypass proxy environment settings: this is an in-cluster Service probe.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while not stop.exists() and time.monotonic() < deadline:
        attempts += 1
        try:
            with opener.open(sys.argv[2], timeout=0.5) as response:
                response.read(1)
                responses += 1
        except urllib.error.HTTPError:
            responses += 1  # Any HTTP response is a failure for wrong-key startup.
        except (urllib.error.URLError, TimeoutError, OSError):
            pass
        time.sleep(0.05)
    result = {"attempts": attempts, "responses": responses, "stopped": stop.exists()}
elif op == "probe-ready":
    result = {"ready": pathlib.Path("/data/probe-ready").exists()}
elif op == "probe-stop":
    pathlib.Path("/data/probe-stop").write_text("stop")
    result = {"stopped": True}
else:
    raise ValueError("unknown maintenance operation")
print(json.dumps(result, sort_keys=True))
