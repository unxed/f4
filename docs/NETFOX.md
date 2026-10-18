# NetFox connection types

The NetFox plugin (the connection manager, and the `sftp://`, `scp://` and `smb://` addresses) offers these connection types. Connections are stored in the manager and reopened from bookmarks, folder history and saved sessions through their URI.

| Type | Address | Notes |
| --- | --- | --- |
| FTP | connection manager | |
| SFTP | `sftp://[user@]host[:port]/path` | key, agent or password |
| SCP | `scp://[user@]host[:port]/path` | opens the SFTP backend (below) |
| SMB | `smb://[domain;]user[:password]@host[:port]/share/path` | Windows and Samba shares |
| FISH+ | connection manager (type `fish+`, `fish` is a synonym) | see [FISH+.md](FISH+.md) |

Saved NetFox connections can store a **Start folder** in Settings Center. When
the connection opens, f4 enters that folder; if it is no longer available, the
connection opens at the server's default folder and records the failure in the
debug log.

## SCP (f4#187)

`scp://` and the connection type **SCP** are the SFTP backend under the name people expect from `scp`: an SSH server that speaks SCP today also speaks SFTP, and SFTP gives real file listing, random access and rename, which the SCP protocol has no wire format for. A host with only the old SCP subsystem and no SFTP is not supported.

Saving an edited file renames a staged temp file onto the original, so SFTP replaces an existing target when the caller asks for overwrite: atomically through `posix-rename@openssh.com` where the server offers it, otherwise by parking the old file under a backup name for the moment of the swap. A plain rename with no overwrite decision still refuses an existing target (f4#1716, `docs/ISSUES/ISSUE_1716_SFTP_SAVE_OVERWRITE.md`).

## SMB (f4#188)

`smb://[domain;]user[:password]@host[:port]/share/path`: the port is 445 unless given, the `DOMAIN;user` form of `smbclient` is understood, and without a user the logon is anonymous. Without a share (`smb://host/`) the panel lists the shares of the server. On systems other than Windows the UNC forms `\\host`, `\\host\share\dir` (and `//host/share` when there is no such local path) typed on the command line or in the path box open the same way. The panel reads, writes, creates folders, renames and deletes, and copies to and from it use the ordinary file operations. Shares themselves are made and removed on the server, not from the panel, and one rename cannot cross two shares (copy and delete instead). It is a pure Go client (`github.com/cloudsoda/go-smb2`), so no `mount` and no external tool is involved. The SMB type is in the full build only; the lite build leaves it out.

Not done yet: a password store or keyring for SMB connections, and Kerberos.

## The Network view (f4#1702)

On Windows, and in every full build elsewhere, **Network** (Commands menu, Ctrl+Alt+Shift+W, or the "Network" drive in Alt+F1) shows the network the way Far Manager's Network panel does. On Windows the system lists it: domains and workgroups, their servers and their shares. Elsewhere the servers are the SMB hosts this session has reached (by `smb://host`, `\\host` or the path below), and a server lists the shares it reports over SMB; a share opens as a directory through the same client as `smb://`. To reach a server that is not in the list yet, type its name as a path in the Network drive (`/nas`); a server that needs a login is opened by its `smb://user:password@host` address instead. The Network view also has an address, `network://` (or `network://server/share/folder`), which works in the command line, a bookmark and the go-to dialog; on Windows a plain `\\server\share` path works as well.
