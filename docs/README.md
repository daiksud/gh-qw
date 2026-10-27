# Documentation

Start with the [README quick start](../README.md#quick-start). Each detailed
contract has one owner; other pages link to it rather than copying the rules.

## For users

| I want to… | Read |
| --- | --- |
| Understand what gh-qw is for and where its scope ends | [Purpose and scope](purpose.md) |
| Find a command, its inputs, results, or safety conditions | [CLI reference](cli.md) |
| Configure roots, the explicit registry, or environment variables | [Configuration reference](configuration.md) |

## For contributors and maintainers

Start with [Contributing](contributing.md) for setup, tests, PRs, and review.

| I want to… | Read |
| --- | --- |
| Understand current component responsibilities and boundaries | [Architecture](architecture.md) |
| Maintain this repository's own settings, labels, and CI | [Repository automation](repository-automation.md) |
| Calculate, publish, verify, or recover a release | [Maintainer releases](releases.md) |

The CLI and configuration references define user-facing behavior. Architecture
describes the implementation; the purpose document owns product direction.
Git history records past v0.x contracts; no ADR archive or migration guide is
maintained.
