# Enron Mail Processor

Reads the [Enron email dataset](https://www.cs.cmu.edu/~enron/) from a compressed TAR archive, stores message headers in SQLite
and serves a list of senders ordered by the number of emails sent over an HTTP API. See [SPEC.md](SPEC.md) for details.

## Development

Requires Go 1.27 or newer.

```sh
go build -o ./dist/processor ./cmd/processor
./dist/processor --path ./enron_mail_20150507.tar.gz
```

Run unit tests:

```sh
go test -race ./...
```

## Running inside Docker container

Build the image:

```sh
docker build -t enron-mail-processor .
```

Run the container:

```sh
docker run --rm -p 8080:8080 -v "$PWD/data:/data" enron-mail-processor --path /data/enron_mail_20150507.tar.gz
```

The container works in `/data`, so the database and `results.txt` end up in the mounted directory. The container runs as
non-root (uid 65532), so the directory must be writable by that user.

## Parameters

| Flag        | Default                        | Description                                                        |
| ----------- | ------------------------------ | ------------------------------------------------------------------ |
| `--path`    | `./enron_mail_20150507.tar.gz` | Path to the archive. The application exits if it does not exist.   |
| `--clean`   | `false`                        | Drop and recreate the database schema on startup.                  |
| `--results` | `true`                         | Write `results.txt` to the working directory after each ingestion. |
| `--db`      | `./enron.db`                   | Path to the SQLite database.                                       |
| `--addr`    | `:8080`                        | HTTP listen address.                                               |

Boolean flags take a value with `=`, for example `--results=false`.

## API

Ingestion does not begin until it is started through the API.

```sh
curl -X POST localhost:8080/start                        # start ingestion
curl localhost:8080/status                               # progress of the latest run
curl 'localhost:8080/top-senders?limit=10&offset=0'      # senders by messages sent
```
