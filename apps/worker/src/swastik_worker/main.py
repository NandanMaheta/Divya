"""HTTP entrypoint. Cloud Scheduler and the Go API call these endpoints; no public traffic."""

from typing import Literal

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="Swastik Worker", version="0.1.0")


class Health(BaseModel):
    status: Literal["ok"]


@app.get("/healthz")
def healthz() -> Health:
    """Liveness probe."""
    return Health(status="ok")
