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

There over 500k files in total (see `find ./maildir -type f | wc -l`).


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


## Initial implementation

Prompt given to Claude:

```
Read the SPEC.md and implement basic version of the processor with Golang. Write unit tests for parsing EML files and the TAR file. Don't write integration tests or tests that require database.

Make sure to read AGENTS.md
```

Claude pointed out few mistakes I made to test files (duplicate IDs and header fields). Fixed those manually and let it do its magic.

Notes:

- Code seems to be valid in eyes of a Java developer
- One direct dependency added: https://pkg.go.dev/modernc.org/sqlite@v1.61.0
- Go has built-in functions for parsing EML files
- The [Dockerfile](../Dockerfile) is direct copy from https://docs.docker.com/guides/golang/#multi-stage-builds
- Claude decided that users are `UPSERT`ed to the database which causes the name to update on every write. Not good.
- Claude added extra index to the schema:

```sql
CREATE INDEX IF NOT EXISTS email_from_user_id ON email (from_user_id);
```

It should improve read times but may cause inserts to be slower. I will test this later.

The first ingestion run took almost 10 minutes. Not good. While processor and memory was lightly utilized, IO's write usage was between
4-6 MB/s. Hopefully the SQLite database isn't the bottleneck...

Results:

```
message_count: 250703
user_count: 87675
sender_count: 20326
messages_processed: 517401
processing_errors: 27
duplicates_ignored: 266671
time_took: 582575
```

51 % of messages were marked as duplicates. 🤯

The amount of senders is also huge. It's something worth of investigating because the archive has only 150 user folders...

All of 27 errors were because of malformed header lines. I'm not going to write fix for these because it's such small amount and I'm not
building the perfect processor. The underlying error is coming from built-in `mail.ReadMessage` function.

```
file=maildir/whalley-g/calendar/142. error="read headers: malformed header line: \"(Maureen x31808)\""
file=maildir/whalley-g/calendar/139. error="read headers: malformed header line: \"Leslie Rich, VP & Doris Kelly Waltkins, SVP, Portfolio Mgrs w/\""
file=maildir/whalley-g/calendar/128. error="read headers: malformed header line: \"Oachim Emanuelsson, VP - Europe of Kiodex\""
file=maildir/whalley-g/test/calendar/79. error="read headers: malformed header line: \"Oachim Emanuelsson, VP - Europe of Kiodex\""
file=maildir/whalley-g/test/calendar/90. error="read headers: malformed header line: \"Leslie Rich, VP & Doris Kelly Waltkins, SVP, Portfolio Mgrs w/\""
file=maildir/whalley-g/test/calendar/93. error="read headers: malformed header line: \"(Maureen x31808)\""
file=maildir/lenhart-m/deleted_items/351. error="read headers: malformed header line: \"!\""
file=maildir/schwieger-j/calendar/3. error="read headers: malformed header line: \"EB35c1\""
file=maildir/schwieger-j/calendar/4. error="read headers: malformed header line: \"EB32c2\""
file=maildir/schwieger-j/calendar/6. error="read headers: malformed header line: \"(Lavorato's office)\""
file=maildir/schwieger-j/calendar/2. error="read headers: malformed header line: \"EB24c2\""
file=maildir/schwieger-j/calendar/1. error="read headers: malformed header line: \"EB3270\""
file=maildir/zipper-a/calendar/23. error="read headers: malformed header line: \"Trading on EOL\""
file=maildir/haedicke-m/calendar/37. error="read headers: malformed header line: \"Mark's Office - EB3325\""
file=maildir/kitchen-l/calendar/35. error="read headers: malformed header line: \"Your office (Jessica Ramiez)\""
file=maildir/lokey-t/calendar/28. error="read headers: malformed header line: \"Ned, Tess and Alice\""
file=maildir/kaminski-v/calendar/40. error="read headers: malformed header line: \"Pre-interview reception - Cindy Justice\""
file=maildir/geaccone-t/inbox/537. error="read headers: malformed header line: \"?\""
file=maildir/hendrickson-s/deleted_items/67. error="read headers: malformed header line: \"!\""
file=maildir/baughman-d/calendar/19. error="read headers: malformed header line: \"Friday, September 21st\""
file=maildir/baughman-d/calendar/4. error="read headers: malformed header line: \"Wednesday,  September 19th\""
file=maildir/baughman-d/calendar/5. error="read headers: malformed header line: \"Interconnect\\\"\""
file=maildir/baughman-d/deleted_items/303. error="read headers: malformed header line: \"1. Mom &dad\""
file=maildir/baughman-d/tasks/4. error="read headers: malformed header line: \"1. Mom &dad\""
file=maildir/sager-e/calendar/34. error="read headers: malformed header line: \"Statute of Limitations\""
file=maildir/kuykendall-t/deleted_items/22. error="read headers: malformed header line: \"!\""
file=maildir/campbell-l/inbox/1040. error="read headers: malformed header line: \"?\""
```

API endpoints worked as planned. The top sender returned by the API is "Jeff Dasovich" (`jeff.dasovich(at)enron.com`) with 5498 messages
sent.

Next up: more speed.


## Testing performance

I could manually test how long file ingestion takes but it would be a waste of my time. Instead Go should test the performance using
built-in tools.

I created a subset TAR archive containin around 70k files from top three senders: `jeff.dasovich`, `kay.mann` and `sara.shackleton` and
tasked Claude to write performance tests:

>Write a performance test that ingests test/perf-test-dataset.tar.gz archive. It has 3 user folders and 70k files.

Response:

>I added a performance test in [perf_test.go](../internal/ingest/perf_test.go), and it passes. It ingested all 70,302 files in 1 min 10 s,
>about 1,000 files per second. At this rate the full 517k-file archive would take about 8–9 minutes.

Which is correct. More than half the files are duplicates. The more interesting part was this:

>The likely cause is that every sender and recipient address gets its own SQL statement, and the cc example alone has over 100 addresses.
>Caching user ids in memory and preparing statements once should help a lot ...

I speculated that this could be problem earlier in "Optimizations" section and proposed same fix. It's time to implement it.

### Sidenote about email addresses

Looking data in the `user` table, there are loads of near duplicates such as:

- `cater'.'john@enron.com`
- `cater.john@enron.com`
- `'.''john@enron.com`
- `'.'john@enron.com`
- `e-mail <'.'john@enron.com>`

There 1919 email addresses stored in the database that contain `'.'` ("escaped" dot). Verified with following query:

```sql
select u.email, u.name
from user u
where u.email like '%''.''%';
```

This is likely a problem because there are estimated amount of over 2000 emails containing sender with "escaped" dot:

```sql
select u.email, COUNT(*) sent
from email e
join recipient r on e.id = r.email_id
join user u on u.id = r.user_id
where r.type is null and u.email like '%''.''%'
group by u.id
order by sent desc;
```

I instructed Claude to clean addresses in EML parser:

>Following email addresses should be cleaned in EML parser:
>1. `cater'.'john@enron.com` becomes `cater.john@enron.com` (replace `'.'` with dot)
>2. `e-mail <'.'john@enron.com>` becomes `.john@enron.com` (remove brackets and "e-mail" prefix)
>3. `<"fichter.john"@enron.com>` becomes `fichter.john@enron.com` (strip double quotes around username part)
>
>Write tests for these cases.

There are most likely more email addresses that require cleanup but I don't have time to investigate further.

## Caching user IDs in memory

Prompt:

>Cache IDs of inserted users by email in memory so that inserting rows to the email table doesn't need upsert users to the database.

Response:

>User ids are now cached in memory, and the performance test ran in 15.7 s instead of 69.7 s, about 4.4× faster. That works out to roughly
>2 minutes for the full archive

Using simple cache for users is as effective as though earlier. Speed improvements scale and processing the whole archive now tooks slightly
over 2 minutes.

## Bulk inserting recipients

I noticed that in the `saveMessage` method of [sqlite.go](../internal/store/sqlite.go) recipients are inserted one-by-one to the table. This
is slow for cases where there are tens of rows to be added. Instructing Claude to investigate and fix:

>Does gosqlite support batch inserts? It would be much efficient in cases where message has tens of recipients. If so use bulk inserts in
>`saveMessage` method when inserting recipients.

My hunch was almost correct; bulk inserts are faster but Claude proposed using even better solution: prepared statements with single
inserts. SQL statements are prepared at the start of the transaction and closed at the end.

Results of performance tests:

- **Before**: 15.7 s
- **Prepared statements**: 10.8 s
- **Prepared statements + bulk insert**: 11.8 s

Reason for bulk inserts being slower:

>The bulk insert was slower because its SQL text depends on how many recipients a message has. SQLite therefore has to parse a new statement
>for nearly every message, and it can't reuse the prepared single-row insert. In SQLite, which runs inside the process, inserting one row is
>cheap.

Running whole dataset now takes less than 2 minutes.
