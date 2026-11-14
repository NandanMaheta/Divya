"""Notification: forward 'needs input' notifications to the owner's phone via ntfy.sh if NTFY_TOPIC is set."""

import os
import urllib.request

from lib import read_input

topic = os.environ.get("NTFY_TOPIC")
message = read_input().get("message", "Swastik agent needs your input")
if topic:
    req = urllib.request.Request(f"https://ntfy.sh/{topic}", data=message.encode(), method="POST")
    try:
        urllib.request.urlopen(req, timeout=5)  # noqa: S310 - fixed https host
    except OSError:
        pass
