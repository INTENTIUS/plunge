// Copyright 2026, INTENTIUS.
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

// Argv tests for plunge's hand-written automation commands (INTENTIUS/plunge#29). A fake
// PulumiCommand records what PlungeAPI hands it; no CLI is started.

import * as assert from "assert";
import { CommandResult, PlungeAPI, PulumiCommand } from "../../automation";

interface Recorded {
    args: string[];
    cwd: string;
    env: { [key: string]: string };
}

function recordingApi(): { api: PlungeAPI; rec: Recorded } {
    const rec: Recorded = { args: [], cwd: "", env: {} };
    const fake = {
        run(args: string[], cwd: string, additionalEnv: { [key: string]: string }): Promise<CommandResult> {
            rec.args = args;
            rec.cwd = cwd;
            rec.env = additionalEnv;
            return Promise.resolve(new CommandResult("", "", 0));
        },
    };
    return { api: new PlungeAPI(fake as unknown as PulumiCommand), rec };
}

describe("automation/plunge", () => {
    describe("drift", () => {
        it("passes only the resource by default", async () => {
            const { api, rec } = recordingApi();
            await api.drift({ resource: "urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b" });
            assert.deepStrictEqual(rec.args, ["drift", "--resource", "urn:pulumi:dev::proj::aws:s3/bucket:Bucket::b"]);
        });

        it("maps every option to its flag", async () => {
            const { api, rec } = recordingApi();
            await api.drift({
                cwd: "/work",
                additionalEnv: { PULUMI_BACKEND_URL: "plunge+file:///tmp/b" },
                color: "never",
                disableIntegrityChecking: true,
                fullyQualifyStackNames: true,
                logflow: true,
                logtostderr: true,
                member: "api",
                memprofilerate: 7,
                otelTraces: "file:///tmp/t.json",
                profiling: "prof",
                resource: "urn:r",
                stack: "dev",
                stream: true,
                tracing: "file:/tmp/trace",
                tracingHeader: "h",
                verbose: 3,
            });
            assert.deepStrictEqual(rec.args, [
                "drift",
                "--color",
                "never",
                "--disable-integrity-checking",
                "--fully-qualify-stack-names",
                "--logflow",
                "--logtostderr",
                "--member",
                "api",
                "--memprofilerate",
                "7",
                "--otel-traces",
                "file:///tmp/t.json",
                "--profiling",
                "prof",
                "--resource",
                "urn:r",
                "--stack",
                "dev",
                "--stream",
                "--tracing",
                "file:/tmp/trace",
                "--tracing-header",
                "h",
                "--verbose",
                "3",
            ]);
            assert.strictEqual(rec.cwd, "/work");
            assert.deepStrictEqual(rec.env, { PULUMI_BACKEND_URL: "plunge+file:///tmp/b" });
        });
    });

    describe("stackAdopt", () => {
        it("leaves out --yes when yes is not set", async () => {
            const { api, rec } = recordingApi();
            await api.stackAdopt({ json: true, stack: "dev" });
            assert.deepStrictEqual(rec.args, ["stack", "adopt", "--json", "--stack", "dev"]);
            assert.ok(!rec.args.includes("--yes"));
            assert.ok(!rec.args.includes("yes"));
        });

        it("turns yes into --yes", async () => {
            const { api, rec } = recordingApi();
            await api.stackAdopt({ yes: true });
            assert.deepStrictEqual(rec.args, ["stack", "adopt", "--yes"]);
        });

        it("maps every option to its flag", async () => {
            const { api, rec } = recordingApi();
            await api.stackAdopt({
                cwd: "/work",
                color: "raw",
                disableIntegrityChecking: true,
                file: "deployment.json",
                fullyQualifyStackNames: true,
                json: true,
                logflow: true,
                logtostderr: true,
                memprofilerate: 2,
                otelTraces: "grpc://127.0.0.1:4317",
                profiling: "prof",
                stack: "org/proj/dev",
                tracing: "file:/tmp/trace",
                tracingHeader: "h",
                verbose: 9,
                yes: true,
            });
            assert.deepStrictEqual(rec.args, [
                "stack",
                "adopt",
                "--color",
                "raw",
                "--disable-integrity-checking",
                "--file",
                "deployment.json",
                "--fully-qualify-stack-names",
                "--json",
                "--logflow",
                "--logtostderr",
                "--memprofilerate",
                "2",
                "--otel-traces",
                "grpc://127.0.0.1:4317",
                "--profiling",
                "prof",
                "--stack",
                "org/proj/dev",
                "--tracing",
                "file:/tmp/trace",
                "--tracing-header",
                "h",
                "--verbose",
                "9",
                "--yes",
            ]);
            assert.strictEqual(rec.cwd, "/work");
        });
    });
});
