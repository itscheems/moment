import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AccountName } from "./AccountName";

afterEach(cleanup);

const selectedId = "11d003f31a23262ea0f6cbe9857981a1";

describe("account name verification", () => {
  it("marks a configured account and keeps the account name as plain text", () => {
    render(
      <AccountName identity={{ userId: selectedId, displayName: "Writer" }} />,
    );
    expect(screen.getByText("Writer")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "认证账号" })).toHaveAttribute(
      "data-verification",
      "blue",
    );
  });

  it("does not assign a mark from a matching display name or administrative role", () => {
    const identity = {
      userId: "c".repeat(32),
      displayName: "Writer",
      role: "admin",
    };
    render(<AccountName identity={identity} />);
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText("Writer")).toBeInTheDocument();
  });

  it("resolves the displayed site account while refusing an unresolved author", () => {
    const { rerender } = render(
      <AccountName
        identity={{ profileUserId: selectedId, displayName: "Publisher" }}
      />,
    );
    expect(screen.getByRole("img", { name: "认证账号" })).toBeInTheDocument();
    rerender(
      <AccountName
        identity={{
          userId: null,
          profileUserId: selectedId,
          displayName: "Historical signature",
        }}
      />,
    );
    expect(screen.queryByRole("img")).toBeNull();
  });
});
