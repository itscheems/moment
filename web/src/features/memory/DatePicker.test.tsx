import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DatePicker } from "./DatePicker";

const now = new Date(2026, 7, 11, 12, 0, 0);

afterEach(cleanup);

describe("DatePicker", () => {
  it("selects a canonical date from the compact calendar", () => {
    const onChange = vi.fn();
    render(
      <DatePicker
        id="from"
        label="开始日期"
        now={now}
        onChange={onChange}
        value="2026-08-11"
      />,
    );

    fireEvent.click(
      screen.getByRole("button", { name: "开始日期：2026年8月11日" }),
    );
    const dialog = screen.getByRole("dialog", { name: "开始日期" });
    expect(dialog).not.toBeNull();
    expect(
      within(dialog)
        .getByRole("button", { name: "2026年8月11日" })
        .getAttribute("aria-pressed"),
    ).toBe("true");

    fireEvent.click(
      within(dialog).getByRole("button", { name: "2026年8月12日" }),
    );
    expect(onChange).toHaveBeenCalledWith("2026-08-12");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("moves between months and clears an existing value", () => {
    const onChange = vi.fn();
    render(
      <DatePicker
        id="to"
        label="结束日期"
        now={now}
        onChange={onChange}
        value="2026-08-11"
      />,
    );

    fireEvent.click(
      screen.getByRole("button", { name: "结束日期：2026年8月11日" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "下个月" }));
    expect(screen.getByText("2026年9月")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "清除" }));
    expect(onChange).toHaveBeenCalledWith("");
  });
});
