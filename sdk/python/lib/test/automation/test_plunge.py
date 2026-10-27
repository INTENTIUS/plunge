# Copyright 2026, INTENTIUS.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Argv tests for plunge's hand-written automation commands (INTENTIUS/plunge#29).

A fake PulumiCommand records what PlungeAPI hands it; no CLI is started.
"""

from typing import Any, Optional

from pulumi.automation._cmd import CommandResult
from pulumi.automation.plunge import PlungeAPI


class _RecordingCommand:
    def __init__(self) -> None:
        self.args: list[str] = []
        self.cwd: Optional[str] = None
        self.env: Any = None

    def run(self, args, cwd, additional_env, on_output=None, on_error=None) -> CommandResult:
        self.args = list(args)
        self.cwd = cwd
        self.env = dict(additional_env)
        return CommandResult("", "", 0)


def _api() -> tuple[PlungeAPI, _RecordingCommand]:
    cmd = _RecordingCommand()
    return PlungeAPI(cmd), cmd  # type: ignore[arg-type]


def test_drift_minimal():
    api, cmd = _api()
    api.drift(resource="urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b")
    assert cmd.args == ["drift", "--resource", "urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b"]


def test_drift_all_flags():
    api, cmd = _api()
    api.drift(
        cwd="/work",
        additional_env={"PULUMI_BACKEND_URL": "plunge+file:///tmp/b"},
        color="never",
        disable_integrity_checking=True,
        fully_qualify_stack_names=True,
        logflow=True,
        logtostderr=True,
        member="api",
        memprofilerate=7,
        otel_traces="file:///tmp/t.json",
        profiling="prof",
        resource="urn:r",
        stack="dev",
        stream=True,
        tracing="file:/tmp/trace",
        tracing_header="h",
        verbose=3,
    )
    assert cmd.args == [
        "drift",
        "--color", "never",
        "--disable-integrity-checking",
        "--fully-qualify-stack-names",
        "--logflow",
        "--logtostderr",
        "--member", "api",
        "--memprofilerate", "7",
        "--otel-traces", "file:///tmp/t.json",
        "--profiling", "prof",
        "--resource", "urn:r",
        "--stack", "dev",
        "--stream",
        "--tracing", "file:/tmp/trace",
        "--tracing-header", "h",
        "--verbose", "3",
    ]  # fmt: skip
    assert cmd.cwd == "/work"
    assert cmd.env == {"PULUMI_BACKEND_URL": "plunge+file:///tmp/b"}


def test_stack_adopt_without_yes():
    api, cmd = _api()
    api.stack_adopt(json=True, stack="dev")
    assert cmd.args == ["stack", "adopt", "--json", "--stack", "dev"]
    assert "--yes" not in cmd.args
    assert "yes" not in cmd.args


def test_stack_adopt_yes():
    api, cmd = _api()
    api.stack_adopt(yes=True)
    assert cmd.args == ["stack", "adopt", "--yes"]


def test_stack_adopt_all_flags():
    api, cmd = _api()
    api.stack_adopt(
        cwd="/work",
        color="raw",
        disable_integrity_checking=True,
        file="deployment.json",
        fully_qualify_stack_names=True,
        json=True,
        logflow=True,
        logtostderr=True,
        memprofilerate=2,
        otel_traces="grpc://127.0.0.1:4317",
        profiling="prof",
        stack="org/proj/dev",
        tracing="file:/tmp/trace",
        tracing_header="h",
        verbose=9,
        yes=True,
    )
    assert cmd.args == [
        "stack", "adopt",
        "--color", "raw",
        "--disable-integrity-checking",
        "--file", "deployment.json",
        "--fully-qualify-stack-names",
        "--json",
        "--logflow",
        "--logtostderr",
        "--memprofilerate", "2",
        "--otel-traces", "grpc://127.0.0.1:4317",
        "--profiling", "prof",
        "--stack", "org/proj/dev",
        "--tracing", "file:/tmp/trace",
        "--tracing-header", "h",
        "--verbose", "9",
        "--yes",
    ]  # fmt: skip
    assert cmd.cwd == "/work"
