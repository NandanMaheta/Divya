"""UserPromptSubmit: remind the model of the money rules when the prompt is about money."""

import re

from lib import read_input

prompt = read_input().get("prompt", "")
if re.search(r"settle|ledger|deal|fixed|profit|brokerage|payment|due|paise|₹|rupee", prompt, re.I):
    print("Money work: load the `business-rules` and `money-safety` skills. Integer paise only; "
          "ledger append-only; golden scenarios GS-01..GS-04 must pass.")
