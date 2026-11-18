---
name: forgejo
description: Forgejo workflow conventions for AInsel agents — which Forgejo MCP tools to use, the refiner → developer → reviewer @mention handoff, and the branch, PR title and issue-closing conventions every pull must follow.
---

# Forgejo workflow

This skill is the shared contract between the three agent roles working on a
Forgejo forge: the **refiner** prepares issues, the **developer** implements
them, the **reviewer** reviews the pulls. Everyone works the same forge with the
same conventions, so a pull from one role is immediately readable by the next.
Each role's concrete bot login lives in that role's persona, which the setup
wizard fills in; this document names roles, never handles.

The document is composed of one section per role, each under the heading that
names it. Read your own section as your contract, and the other two as what the
roles you hand work to expect from you. The plugin serves it from the role texts
kept in Settings, so an edit to one role's text changes that section alone.
