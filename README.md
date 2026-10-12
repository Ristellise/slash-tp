# tp

Peer-to-peer file & folder transfer over WebRTC data channels (pion/webrtc). P2P only — Google STUN, no TURN, no signaling server.

## Usage

Sender:

```
$ tp send ./my-folder
session sakura-momiji-42  (137 files, 3.4 GB)
offer code:
tp1#sakura-momiji-42#Xy9...
paste answer code:
```

Send the offer code to the receiver over any channel (chat, email, phone). The receiver answers:

```
$ tp receive
paste offer code:
session sakura-momiji-42
answer code:
tp1#sakura-momiji-42#Zz8...
```

Paste the answer code back to the sender; the transfer starts. The session name (from a Japanese word generator, `word-word-number`) must match on both sides — an easy human check that codes weren't mixed up.

Everything lands in `outdir/<name-of-transferred-path>/`. Single files transfer too.

### Flags

```
tp send   [-c N] [--code-file F] <path>
tp receive [--code-file F] [outdir]
```

- `-c N` — parallel data channels (default 8). Chunks are striped across all channels.
- `--code-file F` — exchange codes via files `F.offer` / `F.answer` instead of copy-paste (handy over ssh).

## How it works

- One `ctrl` data channel carries a JSON manifest + acks; N `data` channels carry binary chunk frames (`fileID | offset | payload`), striped across channels for speed. Each channel is throttled on SCTP buffered amount.
- The session code is a compressed compact encoding of the SDP (ICE credentials, DTLS fingerprint, setup role, candidates) — no signaling server needed.
- Regular files only; symlinks and empty directories are skipped; file permissions are preserved. Interrupted transfers leave partial files behind.

## Limitations

- STUN-only: peers behind symmetric NAT (most mobile/corporate networks) cannot connect without a TURN relay, which is out of scope.
