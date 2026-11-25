#!/usr/bin/env python3
import argparse
import base64
import hashlib
import json
import os
import socket
import struct
import urllib.request
from urllib.parse import urlparse


def read_exact(connection, size):
    chunks = []
    remaining = size
    while remaining:
        chunk = connection.recv(remaining)
        if not chunk:
            raise ConnectionError("WebSocket closed")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def send_frame(connection, payload, opcode=1):
    payload = payload.encode() if isinstance(payload, str) else payload
    mask = os.urandom(4)
    length = len(payload)
    header = bytearray([0x80 | opcode])
    if length < 126:
        header.append(0x80 | length)
    elif length < 65536:
        header.append(0x80 | 126)
        header.extend(struct.pack("!H", length))
    else:
        header.append(0x80 | 127)
        header.extend(struct.pack("!Q", length))
    masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
    connection.sendall(bytes(header) + mask + masked)


def receive_frame(connection):
    first, second = read_exact(connection, 2)
    opcode = first & 0x0F
    length = second & 0x7F
    if length == 126:
        length = struct.unpack("!H", read_exact(connection, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", read_exact(connection, 8))[0]
    if second & 0x80:
        mask = read_exact(connection, 4)
        payload = read_exact(connection, length)
        payload = bytes(
            value ^ mask[index % 4] for index, value in enumerate(payload)
        )
    else:
        payload = read_exact(connection, length)
    return opcode, payload


def cdp_call(websocket_url, method, params):
    parsed = urlparse(websocket_url)
    connection = socket.create_connection((parsed.hostname, parsed.port), timeout=10)
    connection.settimeout(10)
    key = base64.b64encode(os.urandom(16)).decode()
    request = (
        f"GET {parsed.path} HTTP/1.1\r\n"
        f"Host: {parsed.hostname}:{parsed.port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n\r\n"
    )
    connection.sendall(request.encode())
    response = b""
    while b"\r\n\r\n" not in response:
        response += connection.recv(4096)
    if not response.startswith(b"HTTP/1.1 101"):
        raise ConnectionError("WebSocket handshake failed")
    expected = base64.b64encode(
        hashlib.sha1(
            (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()
        ).digest()
    )
    if b"sec-websocket-accept: " + expected.lower() not in response.lower():
        raise ConnectionError("Invalid WebSocket handshake")

    send_frame(
        connection,
        json.dumps(
            {
                "id": 1,
                "method": method,
                "params": params,
            }
        ),
    )
    while True:
        opcode, payload = receive_frame(connection)
        if opcode == 9:
            send_frame(connection, payload, opcode=10)
            continue
        if opcode == 8:
            raise ConnectionError("WebSocket closed before response")
        if opcode != 1:
            continue
        message = json.loads(payload)
        if message.get("id") == 1:
            connection.close()
            if message.get("error"):
                raise RuntimeError(message["error"].get("message", "CDP command failed"))
            return message.get("result", {})


def evaluate(websocket_url, expression):
    result = cdp_call(
        websocket_url,
        "Runtime.evaluate",
        {
            "expression": expression,
            "returnByValue": True,
            "userGesture": True,
        },
    )
    return result.get("result", {}).get("value")


def click_at(websocket_url, x, y):
    cdp_call(
        websocket_url,
        "Input.dispatchMouseEvent",
        {"type": "mouseMoved", "x": x, "y": y},
    )
    cdp_call(
        websocket_url,
        "Input.dispatchMouseEvent",
        {
            "type": "mousePressed",
            "x": x,
            "y": y,
            "button": "left",
            "clickCount": 1,
        },
    )
    cdp_call(
        websocket_url,
        "Input.dispatchMouseEvent",
        {
            "type": "mouseReleased",
            "x": x,
            "y": y,
            "button": "left",
            "clickCount": 1,
        },
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cdp-url", default="http://127.0.0.1:9222")
    parser.add_argument("--meeting", required=True)
    args = parser.parse_args()

    with urllib.request.urlopen(f"{args.cdp_url}/json/list", timeout=10) as response:
        targets = json.load(response)
    meeting_url = f"https://meet.google.com/{args.meeting}"
    expression = r"""(() => {
      const controls = [...document.querySelectorAll(
        'button, [role="button"], [role="menuitem"]'
      )];
      const companion = controls.find(item =>
        /(?:use|start) companion mode/i.test(item.innerText || '')
      );
      const expand = controls.find(item =>
        (item.getAttribute('aria-label') || item.innerText || '').trim() ===
          'Expand'
      );
      const people = controls.find(item => {
        const labelledBy = item.getAttribute('aria-labelledby');
        const label = labelledBy
          ? document.getElementById(labelledBy)?.innerText || ''
          : item.getAttribute('aria-label') || '';
        return /^people$/i.test(label.trim()) &&
          item.getAttribute('aria-expanded') !== 'true';
      });
      const control = companion || expand || people;
      if (!control) return false;
      control.removeAttribute('disabled');
      control.disabled = false;
      const bounds = control.getBoundingClientRect();
      if (!bounds.width || !bounds.height) return false;
      return {
        x: bounds.x + bounds.width / 2,
        y: bounds.y + bounds.height / 2
      };
    })()"""
    clicked = False
    for target in targets:
        if (
            target.get("type") == "page"
            and target.get("url", "").startswith(meeting_url)
            and target.get("webSocketDebuggerUrl")
        ):
            point = evaluate(target["webSocketDebuggerUrl"], expression)
            if point:
                click_at(
                    target["webSocketDebuggerUrl"],
                    point["x"],
                    point["y"],
                )
                clicked = True
    raise SystemExit(0 if clicked else 1)


if __name__ == "__main__":
    main()
