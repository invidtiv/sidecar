# Guides Moved to Skills

The Markdown guides that used to live in this folder were migrated to **skills**.

- Primary skill location for this repo: `.agents/skills/`
- Additional shared skills may be listed in `AGENTS.md`

Legacy guide files are preserved at:

- `docs/guides/deprecated/`

## Quick Skill Tutorial

1. Find a relevant skill
   - Browse the available skill list in `AGENTS.md`
   - Or list local repo skills with `ls .agents/skills`

2. Open the skill instructions
   - Each skill is documented in `SKILL.md`
   - Example: `cat .agents/skills/create-plugin/SKILL.md`

3. Follow the referenced workflow files
   - Skills may point to `references/`, `scripts/`, or templates
   - Prefer using those artifacts directly instead of rewriting from scratch

4. Ask your agent to use a skill explicitly
   - Mention the skill name in your request (for example, `use create-plugin`)
   - If multiple skills apply, name each one and the agent should combine them

## Migration Note

If you find an old guide link, replace it with either:

- the corresponding skill (`.agents/skills/<name>/SKILL.md`), or
- the archived copy under `docs/guides/deprecated/`

