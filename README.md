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

### Performance tests

The performance test ingests `test/perf-test-dataset.tar.gz` into a temporary SQLite database and reports the throughput. It is
behind the `perf` build tag, so it is not run with the unit tests.

The dataset is not in Git and must be created manually. It contains three user folders (about 70k files) taken from the full
archive:

```sh
tar xzf enron_mail_20150507.tar.gz maildir/dasovich-j maildir/mann-k maildir/shackleton-s
tar czf test/perf-test-dataset.tar.gz maildir/dasovich-j maildir/mann-k maildir/shackleton-s
rm -r maildir
```

Run the test:

```sh
go test -tags perf -run TestIngestPerformance -v -count=1 -timeout 10m ./internal/ingest/
```

`-count=1` disables the Go test cache. Without it, a rerun replays the previous result instantly.

The test is skipped if the dataset does not exist.

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

| Flag        | Default                        | Description                                                         |
| ----------- | ------------------------------ | ------------------------------------------------------------------- |
| `--path`    | `./enron_mail_20150507.tar.gz` | Path to the archive. The application exits if it does not exist.    |
| `--clean`   | `false`                        | Drop and recreate the database schema on startup.                   |
| `--results` | `true`                         | Write `results.txt` to the working directory after each ingestion.  |
| `--db`      | `./enron.db`                   | Path to the SQLite database, or `memory` for an in-memory database. |
| `--addr`    | `:8080`                        | HTTP listen address.                                                |

Boolean flags take a value with `=`, for example `--results=false`.

### In-memory database

With `--db memory` the database is kept in memory instead of a file. Nothing is written to disk except `results.txt`, and all
data is lost when the application stops. `--clean` has no effect because the database always starts empty.

An in-memory database uses a single connection, so API requests made during ingestion wait until the current batch of
messages has been committed.

## API

Ingestion does not begin until it is started through the API.

```sh
curl -X POST localhost:8080/start                        # start ingestion
curl localhost:8080/status                               # progress of the latest run
curl 'localhost:8080/top-senders?limit=10&offset=0'      # senders by messages sent
```
