# The Hase: tools, boundaries, knowledge

**English** · [Deutsch](de/hasen.md)

A Hase is a template in `hasen/`. From it the daemon generates one
opencode agent per Auftrag×Hase; the permissions come from the Räume of
the Auftrag, not from the template. `hasenbau describe hase <name>` shows
what a Hase turns into within a concrete Auftrag.

Tools that it calls during a Lauf come from the Schmied and go through a
three-stage release: [tools](tools.md).

## The back channel

Every Hase gets tools with which it writes into the Bau database itself:

- `hasenbau_summary` writes the one line about what the Lauf did. The
  next Lauf of the same Auftrag gets it as context.
- `hasenbau_notiz` records observations along the way; they show up later
  in `hasenbau dig`.
- `hasenbau_tool_request` only exists if a `requests:` Raum is set in
  `hasenbau.yaml`. With it a Hase asks for a tool it is missing for its
  task, instead of looking for a path around its boundaries. The request
  lands as a file under `<requests>/tools/`, the future inbox of the
  Schmied. Without the entry the tool stays out, and the Hase is not
  pointed at it in the prompt either; `hasenbau describe bau` says where
  the Bau currently stands.

Behind it sits an MCP server that opencode starts as `hasenbau mcp`. It
is registered by `hasenbau init`, and every daemon or Lauf start corrects
the entry to the binary that is currently running and says so in the log,
including after a rebuild to a different path.

## The six prohibitions

Every generated agent gets the same six prohibitions, regardless of the
template: `bash`, `webfetch`, `websearch`, `external_directory`, `task`
and `question` are `deny` in its `permission:` block.

That way they never show up in the model's tool list in the first place,
and the Hase does not go looking for a way around them. `task` weighs
heaviest: a subagent would be an agent of its own and would inherit
neither the permissions nor the Raum boundaries.

## Knowledge

A Hase template can ask for background knowledge:

- `knows_hasenbau: true` includes a shipped introduction to Hasenbau:
  vocabulary, sequence, trace structure, boundaries.
- `knowledge: [pfade]` includes files of your own from the Bau.

Both end up in the generated agent and therefore apply to that Hase only.
`instructions` in the `opencode.json`, by contrast, applies workspace
wide to every agent. The introduction sits in the binary rather than in
the Bau on purpose: that way it always matches the installed version
instead of trailing along as an outdated copy.
