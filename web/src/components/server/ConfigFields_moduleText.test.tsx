import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { ConfigFields } from "./ConfigFields";
import type { ConfigField } from "@/lib/validation";

const difficulty: ConfigField = {
  name: "difficulty",
  displayName: "Difficulty",
  description: "How hard",
  type: "enum",
  enum: ["easy", "hard"],
};

const translations: Record<string, string> = {
  "config.difficulty.displayName": "Difficulté",
  "config.difficulty.description": "Niveau",
  "config.difficulty.options.hard": "Difficile",
};

describe("ConfigFields with module text", () => {
  it("shows the translated label, help text and option names", () => {
    render(
      <ConfigFields
        schema={[difficulty]}
        values={{}}
        onChange={() => {}}
        text={(id, fallback) => translations[id] ?? fallback}
      />,
    );
    expect(screen.getByText("Difficulté")).toBeInTheDocument();
    expect(screen.getByText("Niveau")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Difficile" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "easy" })).toBeInTheDocument();
  });

  it("shows the template text when no text accessor is given", () => {
    render(<ConfigFields schema={[difficulty]} values={{}} onChange={() => {}} />);
    expect(screen.getByText("Difficulty")).toBeInTheDocument();
    expect(screen.getByText("How hard")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "hard" })).toBeInTheDocument();
  });
});
