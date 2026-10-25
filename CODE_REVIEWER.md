# For code reviewer

Dear code review, read this document first.


## Good things to know

- Planning, designing and writing notes as well as the [SPEC.md](./SPEC.md) was done by me without help of any LLM
- However Claude Opus 5.5 was extensively used to implemented the ingestion. Almost every prompt has been documented in
  [NOTES.md](./docs/NOTES.md). Prompts that are left out are minor things such as requests to add comments or rename some variable.
- I personally reviewed every line that the LLM produced

I decided this approach for a following reason: I know how to design software but not to write it with Golang. I could have just prompted
Claude to write me a specification as well, but that would not be in spirit of this challenge.


## How to test the implementation

Start of by building a Docker image:

```sh
docker build -t enron-mail-processor .
```

It's also possibly to build native binary and run it instead. Instructions available in [README.md](./README.md).

Start the Docker image with the [start.sh](./start.sh) script:

```sh
./start.sh /path/to/enron_mail_20150507.tar.gz
```

Script checks that the image and the local dataset exists and starts the application. Stop it with `Ctrl+C`.

The implementation has additional mode where ingestion results are stored in memory. It is of course faster, about 2x times faster on my
tests, but results are gone after application is stopped. Start the in-memory version with:

```sh
./start.sh --in-memory /path/to/enron_mail_20150507.tar.gz
```

Start the ingestion using cURL or something similar:

```sh
curl -X POST localhost:8080/start
```

**NOTE:** ingestion always reports 27 warnings because of "malformed header lines". These files are skipped.

Ingestion status can be read with:

```sh
curl -X GET localhost:8080/status
```

The list of top senders can be fetch with:

```sh
curl -X GET localhost:8080/top-senders
```

When ingestion finishes, it prints out `results.txt` file to the root of this repository containing telemetry data.


## Future improvements

Some ideas for future:

- Using more performant Gunzip archive decompressing for example <https://github.com/klauspost/compress/>
- Switch to database that supports multiple writers and assign one worker per user folder to speed up processing
- Better normalization of email addresses. Source material has lots of wonky addresses and values that are not valid email addresses.
- Improved logic for user name parsing (remove irrelevant characters etc.)

If the implementation would be scaled to "huge" (like 500k messages per minute), I would do following:

- Multiple processor instances that are be started and stopped automatically when needed.
- A key-value database, such as Redis, for processors to share cached user IDs.
- A database that accepts multiple writers and can be replicated.
- Message files that fail the processing are moved to separate location to be handled later. The whole file contents should be used in hash
  calculation and used to compare with previously failed files. This way we don't clog up the storage with duplicates.
- Caching the top senders using database features such as materialized views.
