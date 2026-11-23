# Wake retry and mixed-load tasks

- [X] T001 Read sentinel architecture/specification and CI contributor guidance;
  record design and implementation choices before source edits.
- [X] T002 Write retry and mixed-load regression sources before implementation.
- [X] T003 Add bounded shared-operation retry helper and integrate RequestWake.
- [X] T004 Expose mixed-load measurements in the existing CI Go matrix.
- [X] T005 Update sentinel specification with implemented behavior and fixture scope.
- [X] T006 Compile sentinel locally without running tests or lint.
- [X] T007 CI: run sentinel regressions, race checks, coverage and workflow lint;
  red/green runtime verification is deferred to CI by repository policy. (done 2026-11-22: PR #860 merged 2026-11-10)
- [X] T008 Independent review, signed commit and upstream PR (root agent). (done 2026-11-22: PR #860 merged 2026-11-10)
