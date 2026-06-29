import json


def greet(payload):
    """A pure-Python handler. Runs interpreted by gpython, in-process, on a
    goroutine — no cgo, no GIL, no subprocess. json is Go-backed (Go speed)."""
    d = json.loads(payload)
    return json.dumps({"hello": d.get("name", "world"), "lang": "python"})


def add(payload):
    d = json.loads(payload)
    return json.dumps({"sum": d["x"] + d["y"]})
