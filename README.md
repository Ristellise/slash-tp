# tp

Peer-to-peer file & folder transfer over WebRTC data channels (pion/webrtc). P2P only — Google STUN, no TURN, no signaling server.

## Install

```
curl -fsSL --retry 5 --retry-delay 2 https://github.com/Ristellise/slash-tp/releases/latest/download/install.sh | bash
```

Downloads the latest release binary for your OS/arch, verifies its sha256 against the release checksums, and installs into `/usr/local/bin` (or `~/.local/bin`, or `BINDIR` if set). Binaries: linux/darwin/windows × amd64/arm64 on the [releases page](https://github.com/Ristellise/slash-tp/releases).

Or build from source:

```
go build .
```

## Usage

Sender:

```
$ tp send ./my-folder
session kiiro-gensou-rakugo-41  (137 files, 3.4 GB)
offer code:
tp2#44c0c-zhfmn-4chmq-fjq63-kxas9-re0e0-3yzzz-zzg58-...
paste answer code:
```

Send the offer code to the receiver over any channel (chat, email, phone). The receiver answers:

```
$ tp receive
paste offer code:
session kiiro-gensou-rakugo-41
answer code:
tp2#7pv8e-m2d9k-4xqz7-tf3wa-...
```

Paste the answer code back to the sender; the transfer starts. The session name (Japanese words, `word-word-word-number`) is derived from the session secret — if both sides show the same name, you are in the same session.

Everything lands in `outdir/<name-of-transferred-path>/`. Single files transfer too.

### Flags

```
tp send   [-c N] [--code-file F] <path>
tp receive [--code-file F] [outdir]
```

- `-c N` — parallel data channels (default 8). Chunks are striped across all channels.
- `--code-file F` — exchange codes via files `F.offer` / `F.answer` instead of copy-paste (handy over ssh).

## How it works

- Files are streamed as they are discovered: each `data` channel carries `announce` + `chunk` frames (`type | fileID | offset | payload`) for its files, so there is no folder pre-scan and transfers start immediately regardless of tree size. Each channel is throttled on SCTP buffered amount. The `ctrl` channel only carries end/ack.
- The session code is a magic string: a 16-byte random seed plus the ICE candidates, Crockford-base32 encoded (~110-300 chars). Both sides derive the ICE credentials, DTLS certificate, fingerprint and session name from the seed — so the SDP itself never travels, and possession of the code is the session secret. No signaling server needed.
- Regular files only; symlinks and empty directories are skipped; file permissions are preserved. Interrupted transfers leave partial files behind.

## Limitations

- STUN-only: peers behind symmetric NAT (most mobile/corporate networks) cannot connect without a TURN relay, which is out of scope.
