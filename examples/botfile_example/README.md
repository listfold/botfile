# botfile_example

A minimal botfile source tree, used by the tutorial at
[botfile.org](https://botfile.org) as a stand-in for a shared team repo.

It follows the source grammar `<plugin>/<kind>/<component>`:

```
standards/
  skills/echo/SKILL.md
  instructions/no-em-dashes.md
```

Declare it as a source in `~/.config/botfile/config.toml` and run
`botfile sync` to symlink the `echo` skill and the `no-em-dashes`
instruction into your agents' native paths.
