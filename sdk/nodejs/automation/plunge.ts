// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// plunge's own commands for the automation API, written by hand (INTENTIUS/plunge#21). The generated
// API in ./interface comes from upstream's CLI spec and is regenerated on every upstream merge; this
// file is never generated, so plunge's commands survive that. PlungeAPI is the generated API plus
// plunge's commands.

import * as process from "process";
import type { CommandResult } from "./cmd";
import { PulumiCommand } from "./cmd";
import { API, BaseOptions } from "./interface";

/** Options for `plunge drift`. */
export interface PlungeDriftOptions extends BaseOptions {
    /** Colorize output. Choices are: always, never, raw, auto */
    color?: string;
    /** Disable integrity checking of checkpoint files */
    disableIntegrityChecking?: boolean;
    /** Show fully-qualified stack names */
    fullyQualifyStackNames?: boolean;
    /** Flow log settings to child processes (like plugins) */
    logflow?: boolean;
    /** Log to stderr instead of to files */
    logtostderr?: boolean;
    /** chant's member name for the stack, written as each record's `member`. Defaults to the stack name */
    member?: string;
    /** Enable more precise (and expensive) memory allocation profiles by setting runtime.MemProfileRate */
    memprofilerate?: number;
    /** Export OpenTelemetry traces to the specified endpoint. Use file:// for local JSON files, grpc:// or https:// for remote collectors */
    otelTraces?: string;
    /** Emit CPU and memory profiles and an execution trace to '[filename].[pid].{cpu,mem,trace}', respectively */
    profiling?: string;
    /** The URN of the resource to read */
    resource: string;
    /** The name of the stack to operate on. Defaults to the current stack */
    stack?: string;
    /** Write chant's drift record v1 NDJSON to stdout */
    stream?: boolean;
    /** Emit tracing to the specified endpoint. Use the `file:` scheme to write tracing data to a local file */
    tracing?: string;
    /** Include the tracing header with the given contents. */
    tracingHeader?: string;
    /** Enable verbose logging (e.g., v=3); anything >3 is very verbose */
    verbose?: number;
}

/** Options for `plunge stack adopt`. */
export interface PlungeStackAdoptOptions extends BaseOptions {
    /** Colorize output. Choices are: always, never, raw, auto */
    color?: string;
    /** Disable integrity checking of checkpoint files */
    disableIntegrityChecking?: boolean;
    /** Adopt this exported deployment instead of the DIY checkpoint at the same root */
    file?: string;
    /** Show fully-qualified stack names */
    fullyQualifyStackNames?: boolean;
    /** Print the classification as JSON; without --yes nothing is written */
    json?: boolean;
    /** Flow log settings to child processes (like plugins) */
    logflow?: boolean;
    /** Log to stderr instead of to files */
    logtostderr?: boolean;
    /** Enable more precise (and expensive) memory allocation profiles by setting runtime.MemProfileRate */
    memprofilerate?: number;
    /** Export OpenTelemetry traces to the specified endpoint. Use file:// for local JSON files, grpc:// or https:// for remote collectors */
    otelTraces?: string;
    /** Emit CPU and memory profiles and an execution trace to '[filename].[pid].{cpu,mem,trace}', respectively */
    profiling?: string;
    /** The name of the stack to operate on. Defaults to the current stack */
    stack?: string;
    /** Emit tracing to the specified endpoint. Use the `file:` scheme to write tracing data to a local file */
    tracing?: string;
    /** Include the tracing header with the given contents. */
    tracingHeader?: string;
    /** Enable verbose logging (e.g., v=3); anything >3 is very verbose */
    verbose?: number;
    /** Skip the confirmation prompt and adopt */
    yes?: boolean;
}

export class PlungeAPI extends API {
    private _plungeCommand: PulumiCommand;

    constructor(command: PulumiCommand) {
        super(command);
        this._plungeCommand = command;
    }

    private __plungeRun(options: BaseOptions, args: string[]): Promise<CommandResult> {
        return this._plungeCommand.run(
            args,
            options.cwd ?? process.cwd(),
            options.additionalEnv ?? {},
            options.onOutput,
            options.onError,
            options.signal,
        );
    }

    /**
     * `plunge drift`.
     *
     * Read one resource's drift with one provider Read.
     *
     * Reads the resource's record and its provider's record from the plunge backend by key, without
     * assembling the stack's snapshot and without a stack-wide refresh, configures the provider from its
     * record, calls Read once, and compares the live outputs with the recorded ones. With `stream` the
     * result is chant's drift record v1 NDJSON on stdout. Needs a plunge+<url> backend.
     */
    drift(options: PlungeDriftOptions): Promise<CommandResult> {
        const __final: string[] = ["drift"];

        if (options.color != null) {
            __final.push("--color", "" + options.color);
        }

        if (options.disableIntegrityChecking) {
            __final.push("--disable-integrity-checking");
        }

        if (options.fullyQualifyStackNames) {
            __final.push("--fully-qualify-stack-names");
        }

        if (options.logflow) {
            __final.push("--logflow");
        }

        if (options.logtostderr) {
            __final.push("--logtostderr");
        }

        if (options.member != null) {
            __final.push("--member", "" + options.member);
        }

        if (options.memprofilerate != null) {
            __final.push("--memprofilerate", "" + options.memprofilerate);
        }

        if (options.otelTraces != null) {
            __final.push("--otel-traces", "" + options.otelTraces);
        }

        if (options.profiling != null) {
            __final.push("--profiling", "" + options.profiling);
        }

        __final.push("--resource", "" + options.resource);

        if (options.stack != null) {
            __final.push("--stack", "" + options.stack);
        }

        if (options.stream) {
            __final.push("--stream");
        }

        if (options.tracing != null) {
            __final.push("--tracing", "" + options.tracing);
        }

        if (options.tracingHeader != null) {
            __final.push("--tracing-header", "" + options.tracingHeader);
        }

        if (options.verbose != null) {
            __final.push("--verbose", "" + options.verbose);
        }

        return this.__plungeRun(options, __final);
    }

    /**
     * `plunge stack adopt`.
     *
     * Adopt a stock Pulumi stack into plunge's per-resource records.
     *
     * Runs against a plunge backend (plunge+<diy url>). With `json` the classification is printed as one
     * JSON document (docs/plunge/ADOPT.md); without `yes` nothing is written. A refused adoption prints
     * the document with its refusals and exits non-zero. `yes` is an option here, unlike the generated
     * commands, which always pass --yes: it is what turns a classification into an adoption.
     */
    stackAdopt(options: PlungeStackAdoptOptions): Promise<CommandResult> {
        const __final: string[] = ["stack", "adopt"];

        if (options.color != null) {
            __final.push("--color", "" + options.color);
        }

        if (options.disableIntegrityChecking) {
            __final.push("--disable-integrity-checking");
        }

        if (options.file != null) {
            __final.push("--file", "" + options.file);
        }

        if (options.fullyQualifyStackNames) {
            __final.push("--fully-qualify-stack-names");
        }

        if (options.json) {
            __final.push("--json");
        }

        if (options.logflow) {
            __final.push("--logflow");
        }

        if (options.logtostderr) {
            __final.push("--logtostderr");
        }

        if (options.memprofilerate != null) {
            __final.push("--memprofilerate", "" + options.memprofilerate);
        }

        if (options.otelTraces != null) {
            __final.push("--otel-traces", "" + options.otelTraces);
        }

        if (options.profiling != null) {
            __final.push("--profiling", "" + options.profiling);
        }

        if (options.stack != null) {
            __final.push("--stack", "" + options.stack);
        }

        if (options.tracing != null) {
            __final.push("--tracing", "" + options.tracing);
        }

        if (options.tracingHeader != null) {
            __final.push("--tracing-header", "" + options.tracingHeader);
        }

        if (options.verbose != null) {
            __final.push("--verbose", "" + options.verbose);
        }

        if (options.yes) {
            __final.push("--yes");
        }

        return this.__plungeRun(options, __final);
    }
}
