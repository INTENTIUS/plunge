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

"""
plunge's own commands for the automation API, written by hand (INTENTIUS/plunge#21).

The generated API in pulumi.automation.interface comes from upstream's CLI spec and is regenerated on
every upstream merge. This module is never generated, so plunge's commands survive that. PlungeAPI is
the generated API plus plunge's commands.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping
from typing import Any, Optional

from pulumi.automation._cmd import CommandResult
from pulumi.automation.interface import API


class PlungeAPI(API):
    def drift(
        self,
        *,
        resource: str,
        color: Optional[str] = None,
        disable_integrity_checking: bool = False,
        fully_qualify_stack_names: bool = False,
        logflow: bool = False,
        logtostderr: bool = False,
        member: Optional[str] = None,
        memprofilerate: Optional[int] = None,
        otel_traces: Optional[str] = None,
        profiling: Optional[str] = None,
        stack: Optional[str] = None,
        stream: bool = False,
        tracing: Optional[str] = None,
        tracing_header: Optional[str] = None,
        verbose: Optional[int] = None,
        cwd: Optional[str] = None,
        additional_env: Optional[Mapping[str, str]] = None,
        on_output: Optional[Callable[[str], Any]] = None,
        on_error: Optional[Callable[[str], Any]] = None,
    ) -> CommandResult:
        """Run `plunge drift`.

        Read one resource's drift with one provider Read.

        Reads the resource's record and its provider's record from the plunge backend by key, without
        assembling the stack's snapshot and without a stack-wide refresh, configures the provider from its
        record, calls Read once, and compares the live outputs with the recorded ones. With `stream` the
        result is chant's drift record v1 NDJSON on stdout. Needs a plunge+<url> backend.

        :param resource: The URN of the resource to read
        :param color: Colorize output. Choices are: always, never, raw, auto
        :param disable_integrity_checking: Disable integrity checking of checkpoint files
        :param fully_qualify_stack_names: Show fully-qualified stack names
        :param logflow: Flow log settings to child processes (like plugins)
        :param logtostderr: Log to stderr instead of to files
        :param member: chant's member name for the stack, written as each record's `member`. Defaults to the stack name
        :param memprofilerate: Enable more precise (and expensive) memory allocation profiles by setting runtime.MemProfileRate
        :param otel_traces: Export OpenTelemetry traces to the specified endpoint. Use file:// for local JSON files, grpc:// or https:// for remote collectors
        :param profiling: Emit CPU and memory profiles and an execution trace to '[filename].[pid].{cpu,mem,trace}', respectively
        :param stack: The name of the stack to operate on. Defaults to the current stack
        :param stream: Write chant's drift record v1 NDJSON to stdout
        :param tracing: Emit tracing to the specified endpoint. Use the `file:` scheme to write tracing data to a local file
        :param tracing_header: Include the tracing header with the given contents.
        :param verbose: Enable verbose logging (e.g., v=3); anything >3 is very verbose
        :param cwd: Working directory to run the command in.
        :param additional_env: Additional environment variables to set when running the command.
        :param on_output: A callback to invoke when the command outputs stdout data.
        :param on_error: A callback to invoke when the command outputs stderr data."""
        __final = ["drift"]
        if color is not None:
            __final.extend(["--color", str(color)])
        if disable_integrity_checking:
            __final.append("--disable-integrity-checking")
        if fully_qualify_stack_names:
            __final.append("--fully-qualify-stack-names")
        if logflow:
            __final.append("--logflow")
        if logtostderr:
            __final.append("--logtostderr")
        if member is not None:
            __final.extend(["--member", str(member)])
        if memprofilerate is not None:
            __final.extend(["--memprofilerate", str(memprofilerate)])
        if otel_traces is not None:
            __final.extend(["--otel-traces", str(otel_traces)])
        if profiling is not None:
            __final.extend(["--profiling", str(profiling)])
        __final.extend(["--resource", str(resource)])
        if stack is not None:
            __final.extend(["--stack", str(stack)])
        if stream:
            __final.append("--stream")
        if tracing is not None:
            __final.extend(["--tracing", str(tracing)])
        if tracing_header is not None:
            __final.extend(["--tracing-header", str(tracing_header)])
        if verbose is not None:
            __final.extend(["--verbose", str(verbose)])
        return self._run(
            __final,
            cwd=cwd,
            additional_env=additional_env,
            on_output=on_output,
            on_error=on_error,
        )

    def stack_adopt(
        self,
        *,
        color: Optional[str] = None,
        disable_integrity_checking: bool = False,
        file: Optional[str] = None,
        fully_qualify_stack_names: bool = False,
        json: bool = False,
        logflow: bool = False,
        logtostderr: bool = False,
        memprofilerate: Optional[int] = None,
        otel_traces: Optional[str] = None,
        profiling: Optional[str] = None,
        stack: Optional[str] = None,
        tracing: Optional[str] = None,
        tracing_header: Optional[str] = None,
        verbose: Optional[int] = None,
        yes: bool = False,
        cwd: Optional[str] = None,
        additional_env: Optional[Mapping[str, str]] = None,
        on_output: Optional[Callable[[str], Any]] = None,
        on_error: Optional[Callable[[str], Any]] = None,
    ) -> CommandResult:
        """Run `plunge stack adopt`.

        Adopt a stock Pulumi stack into plunge's per-resource records.

        Runs against a plunge backend (plunge+<diy url>). With `json` the classification is printed as one
        JSON document (docs/plunge/ADOPT.md); without `yes` nothing is written. A refused adoption prints
        the document with its refusals and exits non-zero. `yes` is an option here, unlike the generated
        commands, which always pass --yes: it is what turns a classification into an adoption.

        :param color: Colorize output. Choices are: always, never, raw, auto
        :param disable_integrity_checking: Disable integrity checking of checkpoint files
        :param file: Adopt this exported deployment instead of the DIY checkpoint at the same root
        :param fully_qualify_stack_names: Show fully-qualified stack names
        :param json: Print the classification as JSON; without --yes nothing is written
        :param logflow: Flow log settings to child processes (like plugins)
        :param logtostderr: Log to stderr instead of to files
        :param memprofilerate: Enable more precise (and expensive) memory allocation profiles by setting runtime.MemProfileRate
        :param otel_traces: Export OpenTelemetry traces to the specified endpoint. Use file:// for local JSON files, grpc:// or https:// for remote collectors
        :param profiling: Emit CPU and memory profiles and an execution trace to '[filename].[pid].{cpu,mem,trace}', respectively
        :param stack: The name of the stack to operate on. Defaults to the current stack
        :param tracing: Emit tracing to the specified endpoint. Use the `file:` scheme to write tracing data to a local file
        :param tracing_header: Include the tracing header with the given contents.
        :param verbose: Enable verbose logging (e.g., v=3); anything >3 is very verbose
        :param yes: Skip the confirmation prompt and adopt
        :param cwd: Working directory to run the command in.
        :param additional_env: Additional environment variables to set when running the command.
        :param on_output: A callback to invoke when the command outputs stdout data.
        :param on_error: A callback to invoke when the command outputs stderr data."""
        __final = ["stack", "adopt"]
        if color is not None:
            __final.extend(["--color", str(color)])
        if disable_integrity_checking:
            __final.append("--disable-integrity-checking")
        if file is not None:
            __final.extend(["--file", str(file)])
        if fully_qualify_stack_names:
            __final.append("--fully-qualify-stack-names")
        if json:
            __final.append("--json")
        if logflow:
            __final.append("--logflow")
        if logtostderr:
            __final.append("--logtostderr")
        if memprofilerate is not None:
            __final.extend(["--memprofilerate", str(memprofilerate)])
        if otel_traces is not None:
            __final.extend(["--otel-traces", str(otel_traces)])
        if profiling is not None:
            __final.extend(["--profiling", str(profiling)])
        if stack is not None:
            __final.extend(["--stack", str(stack)])
        if tracing is not None:
            __final.extend(["--tracing", str(tracing)])
        if tracing_header is not None:
            __final.extend(["--tracing-header", str(tracing_header)])
        if verbose is not None:
            __final.extend(["--verbose", str(verbose)])
        if yes:
            __final.append("--yes")
        return self._run(
            __final,
            cwd=cwd,
            additional_env=additional_env,
            on_output=on_output,
            on_error=on_error,
        )
