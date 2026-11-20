# mailexpire

Schedule mail deletion by RFC Message-ID and clean expired messages over IMAP.

## Usage

```sh
mailexpire -r 10d '<first@example.org>' '<second@example.org>'
mailexpire -clean -dry-run
mailexpire -clean
```

- Registration is local; no configuration or credentials are needed.
- Retention is a positive integer with `h`, `d` (24 hours), or `w` (7 days).
- Registering an ID again replaces its expiration, measured from registration.
- Put flags before IDs, pass IDs separately, and quote angle brackets.

## Configuration

Create a template at `~/.mailexpire/config.toml`, then fill in its values:

```sh
mailexpire -init-config
```

```toml
addr = "imap.example.org:993"
user = "me@example.org"
pass = "~/.secrets/imap-password"
mailboxes = ["INBOX", "Archive"]
```

- `pass` is a password-file path; surrounding whitespace in the password is trimmed.
- Leading `~/` in `pass` expands to home. Environment variables are not expanded.
- Use `-config /path/to/config.toml` for an alternate config during initialization or cleanup.
- Initialization never overwrites existing files. Cleanup requires a configured account and nonempty mailbox list.

## Cleanup

- State: `~/.mailexpire/retention.tsv`, containing whole-second UTC expirations (e.g. `2026-11-21T01:03:25Z`) and IDs.
- Searches all configured mailboxes and deletes every exact Message-ID match.
- Requires verified TLS and `UIDPLUS` for targeted expunge, avoiding unrelated messages.
- Successful cleanup removes expired entries, including IDs absent from all configured mailboxes.
- IMAP failures retain the list and exit nonzero; earlier deletions may have completed.
- Dry run changes neither messages nor the retention list.
- Exit codes: `0` success, `69` timeouts or temporary network failures, `1` other errors (including IMAP rejections).

Run hourly with cron:

```cron
0 * * * * /home/me/go/bin/mailexpire -clean
```

## Build and Test

```sh
make test
make build
make install
```
