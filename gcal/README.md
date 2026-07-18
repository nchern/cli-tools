# gcal

Small command line tool that reads Google Calendar and prints events from a configurable time window.

## Setup

1. Create an OAuth desktop app in Google Cloud Console.
2. Enable the Google Calendar API for the project.
3. Download the OAuth client JSON as `credentials.json` into the app config directory.
4. Run the tool and follow the browser authorization prompt:

```sh
go run .
```

The OAuth credentials and token are stored in your OS config path, usually `~/.config/gcal/credentials.json` and `~/.config/gcal/token.json` on Linux.

## Build

```sh
go build -o gcal .
```

## Usage

```sh
gcal [-credentials credentials.json] [-token token.json] [-calendar primary] [-since -100] [-until 30] [-max 10]
```

Examples:

```sh
gcal
gcal -calendar your_calendar_id@example.com
gcal -since -1 -until 3 -max 10

gcal -h # prints help
```

