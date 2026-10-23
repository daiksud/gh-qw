# Purpose and scope

gh-qw reduces the effort of thinking about working directories, so developers
can focus on the work inside them.

## The user problem

A developer working across GitHub repositories and parallel tasks needs to know
whether a repository is already cloned, where it lives, which directory belongs
to each task, and what can be removed when the task is finished. Without a
consistent arrangement, starting or resuming work requires searching for paths
and choosing destinations; finishing it requires identifying the right places
to clean up. These are directory-management decisions, not the development work
the user intended to do.

The need is: **when I know the repository and the work I want to do, help me
prepare, find, and clean up its local working directory without relying on
remembered paths or a new layout decision every time.**

A working directory here means a repository checkout or a linked Git worktree,
not a provisioned development environment.

## What becomes easier

| User activity | Burden gh-qw reduces | Current support |
| --- | --- | --- |
| Start work | Check for an existing clone and choose where to put a missing one. | `get` reuses an existing checkout or clones into the configured layout, then returns its path. |
| Work in parallel | Choose and keep track of a separate directory for another task. | `worktree add` places a linked worktree at a predictable, user-named slot. |
| Resume work | Remember a full path or search directories across repositories. | Repository and worktree listings expose identities and paths that other tools can use. |
| Finish work | Identify the intended cleanup target and understand what was removed. | Removal and prune operations apply their documented selection and safety rules and report failures or partial results. |

The value is the consistent arrangement across repositories and worktrees, not
merely wrapping Git commands. A slot identifies a place; gh-qw does not infer a
task from it, track task completion, or require it to follow every branch switch.
Exact command behavior belongs in the [CLI reference](cli.md), not here.

## Scope and boundaries

Git owns branches and worktree registrations. gh owns GitHub authentication and
network operations. gh-qw adds predictable placement, cross-repository discovery,
and managed cleanup using those tools, without redefining their authentication
or branch semantics. It does not keep a second registry alongside directories
and Git registrations.

Return useful identities and paths for composition with existing tools. Shell
navigation, interactive selection, and editors can consume those results; gh-qw
does not need to own those interfaces. It is not an editor, a development
environment provisioner, a task or agent orchestrator, or a system for choosing
branch strategies and PR workflows.

Reducing mental overhead must not trade away users' work. Keep management
boundaries, target identity, confirmation and revalidation where required, and
honest reporting when an operation only partly succeeds. Safety is a condition
of useful cleanup, not a reason to turn gh-qw into a general safety framework.
See [Architecture](architecture.md) for implementation responsibilities.

## Choosing changes

Use these questions when considering a feature, removal, or refactoring:

1. **Which user task does this help?** Name the preparation, discovery, or cleanup
   scenario and the decision or manual step it removes. Reliability fixes can
   instead name the current safety or failure-handling contract they protect.
2. **Is this gh-qw's responsibility?** Prefer native Git/gh behavior and
   composition with other tools. Avoid extra setup, configuration, metadata,
   dependencies, or new concepts unless their benefit outweighs that burden.
3. **Can the result be demonstrated?** Check the user-visible outcome and
   failure behavior with concrete scenarios. For internal refactoring, explain
   how it makes the same behavior easier to understand, test, and maintain.

An existing feature is not justified merely because it already exists, and a
shorter implementation is not automatically better. Evaluate proposed changes
against this purpose without silently changing the current CLI contract. Update
the owning specification and tests when an approved behavior change is needed.

## What success looks like

Use repeatable start, parallel-work, resume, and cleanup scenarios to assess
whether users make fewer placement decisions, search for or retype fewer paths,
and perform fewer manual steps to identify the right target. They should still
know what changed and what remains after a failure, without losing unrelated
work or taking on more bookkeeping.

Command counts, feature counts, and deleted lines are not measures of user
benefit. These are evaluation criteria, not claims of measured improvements.
Keep this document focused on the current purpose; command contracts, technical
design, and contributor procedures remain in their owning documents.
