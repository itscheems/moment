import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useState } from "react";
import { useDiscussionIdentity } from "./useDiscussionIdentity";

function Draft() {
  const [text, setText] = useState("");
  return (
    <textarea
      aria-label="讨论草稿"
      value={text}
      onChange={(event) => setText(event.target.value)}
    />
  );
}
function Discussion({
  accountId,
  state,
}: {
  accountId: string | null;
  state: "signed-in" | "checking" | "guest" | "expired" | "unavailable";
}) {
  const identity = useDiscussionIdentity(accountId, state);
  return (
    <div hidden={identity.confirming}>
      <Draft key={identity.accountKey ?? "guest"} />
    </div>
  );
}
afterEach(cleanup);

describe("discussion identity confirmation", () => {
  it("retains a same-account draft while an adopted session resolves its identity", () => {
    const view = render(<Discussion accountId="alice" state="signed-in" />);
    fireEvent.change(screen.getByLabelText("讨论草稿"), {
      target: { value: "属于 Alice 的草稿" },
    });
    view.rerender(<Discussion accountId={null} state="signed-in" />);
    expect(screen.getByLabelText("讨论草稿")).not.toBeVisible();
    view.rerender(<Discussion accountId="alice" state="signed-in" />);
    expect(screen.getByLabelText("讨论草稿")).toHaveValue("属于 Alice 的草稿");
  });
  it("clears the retained draft after a different account is confirmed", () => {
    const view = render(<Discussion accountId="alice" state="signed-in" />);
    fireEvent.change(screen.getByLabelText("讨论草稿"), {
      target: { value: "Alice private draft" },
    });
    view.rerender(<Discussion accountId={null} state="signed-in" />);
    view.rerender(<Discussion accountId="bob" state="signed-in" />);
    expect(screen.getByLabelText("讨论草稿")).toHaveValue("");
  });
  it("clears a signed-out account immediately", () => {
    const view = render(<Discussion accountId="alice" state="signed-in" />);
    fireEvent.change(screen.getByLabelText("讨论草稿"), {
      target: { value: "private draft" },
    });
    view.rerender(<Discussion accountId={null} state="guest" />);
    expect(screen.getByLabelText("讨论草稿")).toBeVisible();
    expect(screen.getByLabelText("讨论草稿")).toHaveValue("");
  });
  it("hides a retained surface while account verification is unavailable", () => {
    const view = render(<Discussion accountId="alice" state="signed-in" />);
    fireEvent.change(screen.getByLabelText("讨论草稿"), {
      target: { value: "retained while unavailable" },
    });
    view.rerender(<Discussion accountId={null} state="unavailable" />);
    expect(screen.getByLabelText("讨论草稿")).not.toBeVisible();
    view.rerender(<Discussion accountId="alice" state="signed-in" />);
    expect(screen.getByLabelText("讨论草稿")).toHaveValue(
      "retained while unavailable",
    );
  });
});
