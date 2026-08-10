import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("renders the Moment identity and product promise", () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "Moment" })).toBeInTheDocument();
    expect(screen.getByText("Capture now. Find later.")).toBeInTheDocument();
  });
});
