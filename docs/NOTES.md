# Steps taken to solve the challenge

Notes tracking history of the challenge solving. Grouped into different phases. Final specification used for implementation can be found in
[SPEC.md](./SPEC.md).

**Anything written here is from my head. No AI used.**


# Planning phase

## Investigating the TAR archive

First I extracted a three lists from the archive:

- first level folder names (users)
- second level folder names (user defined folders/categories)
- file names under each category

Commands used:

```sh
find ./maildir -mindepth 1 -maxdepth 1 -type d -printf "%f:%p\n" | awk -F: '!seen[$1]++ {print $1}' | sort > ./users.txt
find ./maildir -mindepth 2 -type d -printf "%f:%p\n" | awk -F: '!seen[$1]++ {print $1}' | sort > ./folders.txt
find ./maildir -mindepth 3 -type f -printf "%f:%p\n" | awk -F: '!seen[$1]++ {print $1}' | sort --numeric-sort > ./filenames.txt
```

Source: <https://superuser.com/a/1058867>

From here on it was clear that users are able to create their folders on their own to categorize emails. There are in total 1778 unique
category names. This likely means that the processor cannot just read the "_sent_mail" or similar folder of each user. There are also unique
6 different "sent" folders.

It seems that most of the email files are inside `<user>/<category>` directory structure. However running following command

```sh
find ./maildir -maxdepth 2 -type f
```

reveals 7 files that are directly under user's root folder. This means that email files need to be search recursively starting from user's
root folder.

There over 500k files in total (see `find . -type f | wc -l`).


## Email files

Files are text containing email headers and content itself. Files use following format:

```
Message-ID: <...>
Date: Mon, 14 May 2001 16:39:00 -0700 (PDT)
From: <email address>
<MORE HEADERS>

<CONTENT>
```

Files seems to be using EML standard.

| Name                          | Description                                                       |
| ----------------------------- | ----------------------------------------------------------------- |
| `Message-ID`                  | Unique ID of the message inside brackets.                         |
| `Date`                        | Date formatted as `Mon, 14 May 2001 16:39:00 -0700 (PDT)`         |
| `From`                        | Email address of the sender                                       |
| `To`                          | Email address of the receiver                                     |
| `Subject`                     | Subject                                                           |
| `Cc`                          | Comma-separated list of email addresses for carbon copies         |
| `Bcc`                         | Comma-separated list of email addresses for blind carbon copies   |
| `Mime-Version`                | ?                                                                 |
| `Content-Type`                | Mime content type of the email                                    |
| `Content-Transfer-Encoding`   | ?                                                                 |
| `X-From`                      | Name of the sender (`From`)                                       |
| `X-To`                        | Name of the receiver (`To`)                                       |
| `X-cc`                        | Comma-separated list of `CC` names                                |
| `X-bcc`                       | Comma-separated list of `BCC` names                               |
| `X-Folder`                    | ?                                                                 |
| `X-Origin`                    | ?                                                                 |
| `X-FileName`                  | Optional attachment                                               |

Their might be even more fields or fields such as `X-FileName` that are not available in every file. Processor could write a warning to the
log if unknown header field is found?

Dates seem to all use same format with resolution of 1 second.


## Inspecting email files

`Message-ID`s are ment to be unique identifiers. This is not however true from processor's perspective. There are exactly the same emails
stored inside different folders. Have users copied these or used some kind of categorization filter? Same email files can also be stored in
multiple inboxes: sender and receivers.

In any case `Message-ID` is not unique and cannot be used to filter out duplicate emails. Some other method to filter out duplicates is
required.

Examples of duplicate email files:

- `maildir/arnold-j/_sent_mail/770.`
- `maildir/arnold-j/all_documents/1010.`
- `maildir/arnold-j/discussion_threads/363.`
- `maildir/arnold-j/sent/774.`
- `maildir/griffith-j/all_documents/156.`
- `maildir/griffith-j/discussion_threads/145.`
- `maildir/griffith-j/private_folders/personal/21.`

Extra note: emails users have received contain BCC information. This means that someone has copied these files from the email server itself.


## What fields do we need parse and store for analysis?

I deciced to use simple logic for duplicate detection: calculate a hash of contents and use it to detect duplicates. Hash is calculated from
following header fields: `From`, `To`, `Cc`, `Bcc` and `Date` separated with a `|` (pipe). `Cc` and `Bcc` can be included in the hash as
well because they are available in both versions: sender's and receivers'. Date field is expected to be in standard format already.

Any other fields including `X-*` are ignored because they are either irrelevant or not unique.

Addition to the hash, following fields should be stored for each email:

- Email address (`From`) and name (`X-From`) of the sender (latter for display purposes only)
- `Message-ID` for debugging purposes
- `Date` used to filter out potential duplicates
- Email addresses (`To`, `Cc`, `Bcc`) and equilevant names of receivers. Whether receiver is direct, CC, or BCC should be stored as well


## So... SQL database?

SQLite database should be used because it doesn't require any external services or setup, the database is just an ordinary file. Relations
between emails can be easily stored and used for other kind of analysis as well.

Downside is SQLite's inadequate data types which don't scale well. Doesn't matter in this small project though.

Hopefully Go's SQLite driver supports multithreading...

```sql
CREATE TABLE user (
    id INTEGER NOT NULL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE email (
    id INTEGER NOT NULL PRIMARY KEY,
    message_id TEXT NOT NULL UNIQUE,
    date INTEGER NOT NULL, -- milliseconds since epoch
    from_user_id INTEGER NOT NULL REFERENCES user (id),
    hash TEXT NOT NULL UNIQUE
);

CREATE TABLE recipient (
    email_id INTEGER NOT NULL REFERENCES email (id),
    user_id INTEGER NOT NULL REFERENCES user (id),
    type TEXT -- either: null (direct), cc or bcc
);
```


## Inputs and outputs

Three HTTP API endpoints were required. A `results.txt` is an extra used for debugging.

### The archive

The processor should read compressed TAR archive from a local file system path that is configurable on start. Processing should not start
until user orders so.

### POST /start

Starts the ingestion process by reading a local file. No request body and query parameters. Returns JSON output:

```json
{
    "started_at": "<ISO-8601 TIMESTAMP>"
}
```

On subsequent calls returns the same timestamp unless previous ingestion has finished.

### GET /status

Returns current status of the ingestion. No request body and query parameters. Returns JSON output:

```json
{
    "finished": false,
    "processed_count": 0
}
```

The `finished` property indicates whether latest run has finished or not yet. If there are no previous runs, returns `false`. Number of
processed messages should not be cleared until new ingestion process is started.

### GET /top-senders

Returns a list of users that have send the most amount of messages together with total count of users.

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

### results.txt

Once ingestion has finished, print out a simple text file with telemetry information:

```
messages_found: 0
users_found: 0
senders_found: 0 # there might be external recipients
messages_processed: 0
processing_errors: 0
duplicate_messages: 0
time_took: 0 # milliseconds
```


## The message processing itself

On processor start:

1. Create SQLite database file and initialize the schema
2. Assert that the archive file exists (path to it as CLI flag)

On ingestion start:

1. Set processing starting timestamp and clear end timestamp. Block all subsequent starts.
2. List top-level folders of the archive
3. Go through each folder one by one
4. Recursively search for all files:
    - Store user information to the `user` table. Ignore duplicates
    - Calculate hash from the message date, sender and recipients
    - Insert the message details with the hash to the `recipient` table using `INSERT OR IGNORE` statement
    - If message was not ignored, insert one row per recipient to the `recipient` table referencing email and user
    - Increment files processed count
5. Once all files have been ingested, set the processing end timestamp
6. Write `results.txt`

Steps 5 and 6 are always executed, even if the ingestion fails fatally.

While ingestion is running, API should respond to requests.

**Extra:**

- Performance tests from the start?

**Optimizations:**

- Store user information to the database once and cache which email addresses have been stored already
- Make sure to always read only one file from the archive. Reading all of the contents to memory is wasteful
- Multithreading? Start `n` workers that go through files per user folder
- Remove recipients from the hash calculation?
- Put storing of recipients behind a feature flag?
- Add index to the `email.hash` column?


## Things to do list

Technical things that need to be done.

1. Implement an EML message file parsing function and unit tests for it. The function should return following details:
    - Email address and the name of the sender
    - Message ID
    - Timestamp as milliseconds since epoch
    - A list of recipient email addresses and names
2. Implement a function that reads through a compressed TAR archive file and returns list of top level folders (under root directory). Write
   units for this also. If possible, the whole archive should not be read to the memory.
3. Implement a function that recursively finds files inside TAR archive and returns paths to them. Write unit tests for this.
4. Implement a function that calculates hash from given values. Values can be strings and numbers, and should be separated by `|`.
5. Implement functions for inserting rows to the tables defined earlier. Insertion to the `email` table should be ignored if there is
   already equal hash stored. No tests for these required.
6. Implement a function that returns ordered list of users with number of messages they have sent. The function should have two parameters:
   `limit` and `offset`. No tests required.
7. Implement basic version of message file processing detailed earlier. Processing should be executed on different thread so that it doesn't
   block HTTP API requests. Don't write tests and ignore optimizations.
8. Implement HTTP API endpoints detailed earlier. Don't write tests.

