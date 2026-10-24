# Technical Specification

Specification for AI agent to read and implement.

## Objective

A processor, written with Golang, should be able to read and extract contents of a compressed TAR archive and provide a list of senders
ordered by a number of emails sent. The archive contains in total of 517 401 email files from 150 users organized into folders.

The processor should start the message processing when ordered to do so.

Source: <https://www.cs.cmu.edu/~enron/>

## The application

Golang application running inside a Docker container. Arguments can be passed to the application on startup.

On startup the application checks that the archive exists in given `--path` and creates empty SQLite database and initializes schema to it.
If the archive doesn't exists, application shall exit. Pre-existing database is left untouched unless `--clean` argument is passed.


## The input

### The TAR archive

The archive has following directory structure:

```
maildir/
├─ user1/
│  ├─ category_a
|  |  ├─ 1. (file)
|  |  ├─ 2. (file)
|  |  └─ 3. (file)
│  ├─ category_b
|  |  ├─ 1. (file)
|  |  └─ 2. (file)
│  └─ 1. (file)
└─ user2/
   ├─ category_a
   |  ├─ 1. (file)
   |  ├─ 2. (file)
   |  └─ 3. (file)
   └─ category_c
      └─ 1. (file)
```

Files can be under user's root folder or in any subfolder. Each file is a text file in EML format.

The processor shall read the file only once ordered to do so.

TAR archive reading should simple have unit tests to test that all files are read from the archive. Archive used in tests can be found in
`test/` directory.


## Email files

Files are text containing email headers and content itself using EML standard format:

```
Message-ID: <...>
Date: Mon, 14 May 2001 16:39:00 -0700 (PDT)
From: <email address>
<MORE HEADERS>

<CONTENT>
```

Email contents are ignored. Headers were are interested in:

| Name                          | Description                                                               |
| ----------------------------- | ------------------------------------------------------------------------- |
| `Message-ID`                  | Unique ID of the message inside brackets.                                 |
| `Date`                        | Date formatted as `Mon, 14 May 2001 16:39:01 -0700 (PDT)`                 |
| `From`                        | Email address of the sender                                               |
| `To`                          | Email address of the receiver                                             |
| `Cc`                          | Optional comma-separated list of email addresses for carbon copies        |
| `Bcc`                         | Optional comma-separated list of email addresses for blind carbon copies  |
| `X-From`                      | Name of the sender (`From`)                                               |

Message file processing should simple have unit tests. Files to test can be found in `test/` directory.

Email addresses should be normalized to be clean as possible to improve accuracy of sent messages.

## Command-line arguments

- `path`: path to the archive file. Defaults `./enron_mail_20150507.tar.gz`.
- `clean`: when `true`, the database schema is always reinitialized. Defaults to `false`.
- `results`: when `true`, a `results.txt` file is printed after processing stops. Defaults `true`.
- `db`: path to SQLite database or `memory` for in-memory database. Defaults to `enron.db`
- `addr`: HTTP server address. Defaults to `:8080`.

## HTTP API endpoints

### POST /start

Starts the ingestion process by reading the archive. No request body or query parameters. Returns JSON output:

```json
{
    "started_at": "<ISO-8601 TIMESTAMP>"
}
```

On subsequent calls returns the same timestamp in ISO-8601 timestamp format, unless previous ingestion has finished. In other words,
parallel processing is not allowed.

### GET /status

Returns current status of the ingestion. No request body or query parameters. Returns JSON output:

```json
{
    "finished": false,
    "processed_count": 0
}
```

The `finished` property indicates whether latest run has finished or not yet. If there are no previous runs, returns `false`. Number of
processed messages should not be cleared until new ingestion process is started.

### GET /top-senders

Returns a list of users that have send the most amount of messages together with a total count of users.

Has two query parameters:

* `limit`: number of users returned (defaults 10)
* `offset`: number of users to skip (defaults 0)

Returns JSON output:

```json
{
    "total": 150,
    "objects": [
        {
            "id": 1,
            "email": "foo.bar@enron.com",
            "name": "Foo Bar",
            "messages_sent": 100
        },
        {
            "id": 2,
            "email": "...",
            "name": "...",
            "messages_sent": 200
        }
    ]
}
```


## Printing out results

If `--results` flag equals to `true` (default), a simple text file with telemetry information is printed out once the ingestion finishes.

Example contents:

```
message_count: 0
user_count: 0
sender_count: 0
messages_processed: 0
processing_errors: 0
duplicates_ignored: 0
time_took: 0
```

Details:

- `user_count` equals to number of unique users (senders and recipients) found and stored in database
- `sender_count` equals to number of unique users have sent at least one email. There might be external recipients.
- `processing_errors` equals to number of message files failed to process
- `duplicates_ignored` equals to number of duplicate files have been ignored
- `time_took` equals to milliseconds it took from start of the processing to printing of this file


## Storing processed messages

Following database schema is used to store results of message parsing. `email.hash` value is calculated from following header fields `From`,
`To`, `Cc`, `Bcc` and `Date` separated with a `|`. Value of the `Date` is milliseconds in epoch.

```sql
CREATE TABLE user (
    id INTEGER NOT NULL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL -- Name from X-From, X-To, X-cc or X-bcc header field
);

CREATE TABLE email (
    id INTEGER NOT NULL PRIMARY KEY,
    message_id TEXT NOT NULL UNIQUE,
    date INTEGER NOT NULL, -- milliseconds since epoch
    from_user_id INTEGER NOT NULL REFERENCES user (id),
    hash TEXT NOT NULL UNIQUE
);
```


## Message processing

High level logic:

1. Set processing starting timestamp and clear end timestamp. Block all subsequent starts.
2. List top-level folders of the archive
3. Go through each folder one by one
4. Recursively search for all files:
    - Store user information to the `user` table. Ignore duplicates, cache inserted user IDs in memory
    - Calculate hash from the message date, sender and recipients
    - Insert the message details with the hash to the `recipient` table using `INSERT OR IGNORE` statement
    - Increment files processed count
5. Once all files have been ingested, set the processing end timestamp
6. Write `results.txt`

Steps 5 and 6 are always executed, even if the ingestion fails fatally.

While ingestion is running, API should respond to requests.
