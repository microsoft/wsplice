# wsplice [![Build Status](https://travis-ci.org/mixer/wsplice.svg?branch=master)](https://travis-ci.org/mixer/wsplice)

`wsplice` is a websocket multiplexer, allowing you to connect to multiple remote hosts though a single websocket.

### Usage

Download a binary from the [Releases page](https://github.com/mixer/wsplice/releases). You can then run it from the command line. `wsplice` can be configured to use client certification authentication, and/or an allowlist of remote hostnames it's allowed to connect to.

```bash
# Run a publicly accessible instance with client cert auth
./wsplice --tls-cert=my-cert.pem \
    --tls-key=my-key.pem \
    --tls-ca=my-ca.pem \
    --listen=0.0.0.0:3000

# Omit the CA cert to run it over TLS, and allow it to connect
# to example.com and ws.example.com
./wsplice --tls-cert=my-cert.pem \
    --tls-key=my-key.pem \
    --allowed-hostnames="example.com ws.example.com"
```

### Protocol

Websocket frames are prefixed with two bytes, as a big endian uint16, to describe who that message goes to. The magic control index is `[0xff, 0xff]`, which is a simple JSON RPC protocol. To connect to another server, you might do something like this in Node.js:

```js
const payload = Buffer.concat([
    Buffer.from([0xff, 0xff]),
    Buffer.from(JSON.stringify({
        id: 42,
        type: "method",
        method: "connect",
        params: {
            url: "ws://example.com",
            headers: { /* ... */ }, // optional
            subprotocols: [/* ... */], // optional
        }
    }))
]);

websocket.write(payload);
```

The response is a JSON object like:

```json
{
  "id": 42,
  "type": "reply",
  "result": {
    "index": 0
  }
}
```

In this case the socket index is 0. You can send messages to that websocket by prefixing the messages with `0`, encoded as a big endian uint16, and likewise wsplice will proxy and prefix messages that it gets from that server with the same. All frames, with the exception of `ping` and `pong` frames (which are handled automatically for you) will be proxied.

Once the client disconnects, the wsplice will call `onSocketDisconnect`. For example:

```json
{
  "id": 0,
  "type": "method",
  "method": "onSocketDisconnect",
  "params": {
    "code": 4123,
    "message": "The socket close reason, if any",
    "index": 0
  }
}
```

### Performance

`wsplice` spends most time (upwards of 90%) handling network reads/writes; performance is generally bounded by how much data your operating system's kernel and send or receive from a single connection.

| Throughput  | payload=32B | payload=128B | payload=1024B | payload=4098B |
|-------------|-------------|--------------|---------------|---------------|
| clients=32  | 231 mbps    | 235 mbps     | 2940 mbps     | 11200 mbps    |
| clients=128 | 327 mbps    | 333 mbps     | 2520 mbps     | 14600 mbps    |
| clients=512 | 46.3 mbps   | 533 mbps     | 3570 mbps     | 11200 mbps    |
| **Latency** |             |              |               |               |
| clients=32  | 5.6μs       | 4.6μs        | 5.4μs         | 7.4μs         |
| clients=128 | 4.7μs       | 5.2μs        | 5.7μs         | 5.7μs         |
| clients=512 | 3.7μs       | 4.0μs        | 5.2μs         | 7.1μs         |

These measurements were taken on an B8 Azure VM running Ubuntu 16.04, using the binary in `./cmd/bench`.
