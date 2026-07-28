# Disabled upstream workflows

Monad preserves these inherited E2B workflows as merge/rebase reference, but
GitHub must not discover or execute them in the public fork.

They include upstream release, publish, deploy, scheduled, self-hosted-runner,
and umbrella pull-request automation. Some request write or OIDC permissions;
some execute repository code with Docker or elevated host access; and some can
mutate a repository using only `GITHUB_TOKEN`.

Every inherited workflow, including reusable `workflow_call` definitions, is
kept here rather than under `.github/workflows`. This prevents a future
umbrella workflow, renamed upstream trigger, or manual invocation from reaching
an Engram runner or credential by accident.

Repository Actions remain disabled while this quarantine is reviewed and
merged. `.github/workflows/monad-terraform-validation.yml` is the only workflow
permitted on the default branch. It is GitHub-hosted, read-only, SHA-pinned,
performs backend-free Terraform validation on every pull request and main push,
and fails unless it is the sole active workflow file.

An upstream sync must keep these files in this directory until Engram owns a
separately named workflow with an explicit permission, runner, credential,
environment, and rollback design.

Activation order:

1. merge the quarantine while repository Actions remain disabled;
2. verify that the default branch contains exactly the one permitted workflow;
3. restrict the repository to the exact selected, full-SHA-pinned actions,
   require approval for every external contributor, and keep organization or
   shared self-hosted runners unavailable;
4. enable Actions and manually dispatch `Monad Terraform Validation`;
5. require its `Keyless GCP configuration` check on protected `main`; and
6. only then reopen infrastructure changes.
