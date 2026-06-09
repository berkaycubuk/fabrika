- [ ] Merged column is not growing longer with its content.
- [ ] How agent pooling works? Which agents chosen for which work?
- [ ] More visual diff
- [ ] Git viewer for more git integration
- [x] when agents work there is no way to validate they're really working. They might get stuck.
      → Agent liveness: the runner streams output through an activity meter, emits
        per-task heartbeats (live pulse on running cards; amber when quiet), and kills
        an agent silent past `agent_idle_timeout` (default 5m, 0/"off" to disable) as a
        `liveness`-stage failure instead of burning the hard timeout. Process-group kill
        so the whole agent tree dies, not just the shell.
- [ ] Mobile app for mobile usage
- [ ] Desktop app for increased experience ? (question and tinkering)
- [ ] Priority for big tasks
- [ ] Do we need custom skills for fabrika specific?
- [ ] Logging agent comments and thoughts
- [ ] Commenting card movements. example: ready -> running
- [ ] when using multiple fabrika instances, it is hard to see which project I'm working on. Show project name on the UI somewhere.
- [ ] Supporting GitHub issues
- [ ] when a github workflow fails show it on the ui
- [ ] todo list or scratchpad like a feature to tinker
