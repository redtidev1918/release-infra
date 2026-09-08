from __future__ import annotations

import random
import time
from collections.abc import Callable, Iterable
from typing import TypeVar


T = TypeVar("T")


def retry(operation: Callable[[], T], retryable: Callable[[Exception], bool], delays: Iterable[float] = (5, 20, 60), *, sleep: Callable[[float], None] = time.sleep) -> T:
    for delay in delays:
        try:
            return operation()
        except Exception as exc:
            if not retryable(exc):
                raise
            sleep(delay + random.random())
    return operation()
