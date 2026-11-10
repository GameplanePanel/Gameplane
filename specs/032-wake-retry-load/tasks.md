# Wake retry and mixed-load tasks

- [X] T001 Read sentinel architecture/specification and CI contributor guidance;
  record design and implementation choices before source edits.
- [X] T002 Write retry and mixed-load regression sources before implementation.
- [X] T003 Add bounded shared-operation retry helper and integrate RequestWake.
- [X] T004 Expose mixed-load measurements in the existing CI Go matrix.
- [X] T005 Update sentinel specification with implemented behavior and fixture scope.
- [X] T006 Compile sentinel locally without running tests or lint.
- [ ] T007 CI: run sentinel regressions, race checks, coverage and workflow lint;
  red/green runtime verification is deferred to CI by repository policy.
- [ ] T008 Independent review, signed commit and upstream PR (root agent).
