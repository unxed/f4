# MongoDB panel (`plugins/mongofs`)

[f4#1663](https://github.com/unxed/f4/issues/1663), MongoDB part: a server as a drive.

Open it from the drive menu (Alt+F1) as **MongoDB**. Databases and collections are folders; the documents of a collection are `<id>.json` files (relaxed extended JSON, indented) that F3 views and F5 copies out, and that can be edited, created and deleted.

## How

* **No driver.** The official Go driver is a big dependency for what a browser needs. The plugin speaks the wire protocol itself: OP_MSG (MongoDB 3.6+) with a small BSON codec, and `listDatabases`, `listCollections`, `find`, `getMore` and `killCursors`.
* **Connection:** the `MONGODB_URI` environment variable, default `mongodb://127.0.0.1:27017`. `mongodb://[user:pass@]host[:port][/db][?authSource=x&tls=true]`; with a seed list the first host is used. `mongodb+srv://` is resolved through its SRV (and TXT `authSource`) records and uses TLS unless `tls=false`. **Password.** Leave it out (`mongodb://user@host/db`) and the panel asks for it in a masked dialog when it first connects; f4 never stores it. The answer is kept in memory for that panel (and its copies) only, so a reconnect does not ask again, and a wrong one is forgotten and asked for again, three times at most. A password written into `MONGODB_URI` still works, but then it sits in the environment. Authentication is SCRAM: SHA-256 or SHA-1, negotiated with the server (or forced with `authMechanism=`); X.509, Kerberos and replica-set discovery are not supported yet.
* **Names.** An ObjectId `_id` is the 24-digit hex name; a string id is `s_<escaped>`, an integer id `i_<n>`, anything else `j_<escaped json>` (listed and openable while the listing is fresh). Listings ask only for `_id`; a document is fetched when opened.
* **Editing.** F4 on a document opens it in the editor; saving replaces the document (`update`). The text keeps every type: doubles that look like integers are written `3.0`, an int64 that would fit an int32 is `{"$numberLong": "7"}`, and `$oid`, `$date`, `$binary`, `$timestamp` and decimal128 round-trip. Saving under a new name inserts a document (its `_id` from the name when the name is an id, else generated); an `_id` in the text that disagrees with the name is refused. F8 deletes a document, F7 creates a collection. Dropping collections or databases, renaming and changing attributes are not offered.
* **Addresses.** The panel path is `mongo:///<database>/<collection>/<id>.json`, so bookmarks, folder history and saved sessions (f4#1669) bring the panel back through the `mongo://` URI provider; the server is the one `MONGODB_URI` names.
* **Limits.** A collection lists its first 1000 documents and then says the list is partial. Nothing connects until the panel is opened.

## Not yet

Queries/filters, other auth mechanisms, the lite build (full build only, like the Docker and Kubernetes panels).
