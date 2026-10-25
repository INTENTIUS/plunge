# Upstream workflows (inactive)

These are pulumi/pulumi's GitHub Actions workflows, moved here from `.github/workflows` when plunge
forked at upstream `7e12743da8` (INTENTIUS/plunge#3). GitHub only runs workflows in
`.github/workflows`, so nothing here fires on INTENTIUS/plunge: no crons, no release or publish jobs,
no jobs that need pulumi's secrets, runners or package registries.

They stay in the tree so upstream merges apply cleanly: git's rename detection carries upstream edits
to these files into this directory. A workflow that upstream adds lands in `.github/workflows` instead;
`plunge-upstream-guard.yml` fails the merge PR until it is moved here with `git mv`.

`release.pub` moved with them; it is pulumi's release signing key, used only by these workflows.

plunge's own workflows live in `.github/workflows` and are named `plunge-*`.
