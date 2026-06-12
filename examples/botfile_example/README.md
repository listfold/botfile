# botfile_example

A minimal botfile source tree, used by the tutorial at
[botfile.org](https://botfile.org) as a stand-in for a shared team repo.

It follows the source grammar `<plugin>/<kind>/<component>`:

```
botfile/
  skills/bootstrap-botfile/SKILL.md
standards/
  skills/echo/SKILL.md
  instructions/no-em-dashes.md
  commands/changelog.md
```

Declare it as a source in `~/.config/botfile/config.toml` and run
`botfile sync` to symlink its components into your agents' native paths.

The `botfile` plugin's `bootstrap-botfile` skill is botfile's own
operator guide (the same content `botfile guide` prints) packaged as a
skill: an agent that receives it can drive botfile on your behalf.
