"""A test-only SMTP sink with implicit TLS for live browser runs.

It accepts every message and writes each to its own file, so a spec can read what the
installation sent. It authenticates nobody and must only run on a disposable test network.

Usage: smtp_sink.py <output directory> <certificate> <key>
"""

import asyncio
import pathlib
import ssl
import sys
import time

PORT = 465

OUT = pathlib.Path(sys.argv[1])

async def session(reader, writer):
    def say(line):
        writer.write((line + "\r\n").encode())
    say("220 sink ready")
    await writer.drain()
    data, reading = [], False
    while line := await reader.readline():
        text = line.decode("utf-8", "replace").rstrip("\r\n")
        if reading:
            if text == ".":
                reading = False
                (OUT / f"{time.time_ns()}.eml").write_text("\n".join(data))
                data = []
                say("250 stored")
            else:
                data.append(text[1:] if text.startswith("..") else text)
            await writer.drain()
            continue
        verb = text[:4].upper()
        if verb == "EHLO":
            say("250-sink")
            say("250 8BITMIME")
        elif verb == "DATA":
            reading = True
            say("354 go on")
        elif verb == "QUIT":
            say("221 bye")
            await writer.drain()
            break
        else:
            say("250 ok")
        await writer.drain()
    writer.close()

async def main():
    context = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
    context.load_cert_chain(sys.argv[2], sys.argv[3])
    server = await asyncio.start_server(session, "0.0.0.0", PORT, ssl=context)
    async with server:
        await server.serve_forever()

asyncio.run(main())
