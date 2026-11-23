import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import type { ModpackView } from "@/types";
import { ModpackMissingFiles } from "./modpack-missing-files";

describe("ModpackMissingFiles without a missingFiles list", () => {
  it("renders nothing when the view omits missingFiles", () => {
    const view = {
      active: null,
      phase: "",
      message: "",
      backupName: "",
      keyShared: false,
      missingFilesUnknown: false,
      downloadsPath: "",
      backupAvailable: false,
    } as unknown as ModpackView;
    const { container } = render(<ModpackMissingFiles view={view} />);
    expect(container).toBeEmptyDOMElement();
  });
});
