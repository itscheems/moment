import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LanguageDialog } from "../components/LanguageDialog";
import { SessionProvider, useSession } from "../features/auth/SessionProvider";
import { makeAccountSession } from "../test/accountFixtures";
import { LanguageProvider, useLanguage } from "./LanguageProvider";
import { LANGUAGE_STORAGE_KEY } from "./index";

function response(data: unknown, status = 200) {
  return new Response(
    JSON.stringify(
      status === 200
        ? { data, meta: {} }
        : {
            error: {
              code: status === 401 ? "UNAUTHORIZED" : "INTERNAL",
              message: "request failed",
              details: {},
            },
          },
    ),
    { status },
  );
}

function Panel() {
  const { t } = useTranslation();
  const { locale } = useLanguage();
  const auth = useSession();
  const [open, setOpen] = useState(false);
  return (
    <>
      <output data-testid="language">{locale}</output>
      <output data-testid="auth">{auth.state}</output>
      <textarea aria-label="editing content" defaultValue="Unpublished text" />
      <button onClick={() => setOpen(true)}>{t("语言")}</button>
      <button onClick={() => auth.forgetSession(null)}>End session</button>
      {open ? <LanguageDialog onClose={() => setOpen(false)} /> : null}
    </>
  );
}

const show = () =>
  render(
    <SessionProvider>
      <LanguageProvider>
        <Panel />
      </LanguageProvider>
    </SessionProvider>,
  );

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.sessionStorage.clear();
});

describe("language selection", () => {
  it("cancels a visitor draft, then saves without replacing the editing surface", async () => {
    vi.spyOn(window, "fetch").mockImplementation(async () =>
      response(null, 401),
    );
    show();
    await waitFor(() =>
      expect(screen.getByTestId("auth")).toHaveTextContent("guest"),
    );
    const input = screen.getByRole("textbox", { name: "editing content" });
    fireEvent.change(input, { target: { value: "Keep this draft" } });
    fireEvent.click(screen.getByRole("button", { name: "语言" }));
    expect(await screen.findByRole("dialog")).toHaveAccessibleDescription(
      "选择 Moment 的显示语言，保存在当前浏览器中。",
    );
    fireEvent.click(screen.getByRole("radio", { name: /English/ }));
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.getByTestId("language")).toHaveTextContent("zh-CN");
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "语言" }));
    fireEvent.click(screen.getByRole("radio", { name: /English/ }));
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await screen.findByRole("button", { name: "Language" });
    expect(screen.getByRole("textbox", { name: "editing content" })).toBe(
      input,
    );
    expect(input).toHaveValue("Keep this draft");
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe("en");
    expect(document.documentElement.lang).toBe("en");
  });

  it("restores the account choice, surfaces save failures and restores the separate visitor choice on logout", async () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, "zh-CN");
    const account = makeAccountSession();
    let saved: "en" | "zh-CN" = "en";
    let refuse = true;
    const requests: RequestInit[] = [];
    vi.spyOn(window, "fetch").mockImplementation(async (input, options) => {
      if (String(input).endsWith("/session"))
        return response({ ...account, preferences: { ui_locale: saved } });
      requests.push(options ?? {});
      if (refuse) return response(null, 500);
      saved = "zh-CN";
      return response({ ui_locale: saved });
    });
    const first = show();
    await screen.findByRole("button", { name: "Language" });
    fireEvent.click(screen.getByRole("button", { name: "Language" }));
    fireEvent.click(screen.getByRole("radio", { name: /简体中文/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Your language was not saved",
    );
    expect(screen.getByTestId("language")).toHaveTextContent("en");
    expect(screen.getByRole("radio", { name: /简体中文/ })).toBeChecked();
    refuse = false;
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(new Headers(requests[0].headers).get("X-CSRF-Token")).toBe(
      account.csrf_token,
    );
    expect(JSON.parse(String(requests[0].body))).toEqual({
      ui_locale: "zh-CN",
    });
    first.unmount();
    show();
    await waitFor(() =>
      expect(screen.getByTestId("auth")).toHaveTextContent("signed-in"),
    );
    expect(screen.getByTestId("language")).toHaveTextContent("zh-CN");
    fireEvent.click(screen.getByRole("button", { name: "End session" }));
    expect(screen.getByTestId("language")).toHaveTextContent("zh-CN");
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe("zh-CN");
  });

  it("does not apply a late save after the account session ends", async () => {
    const account = makeAccountSession();
    let finish: ((value: Response) => void) | undefined;
    vi.spyOn(window, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/session")) return response(account);
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    });
    show();
    await waitFor(() =>
      expect(screen.getByTestId("auth")).toHaveTextContent("signed-in"),
    );
    fireEvent.click(screen.getByRole("button", { name: "语言" }));
    fireEvent.click(screen.getByRole("radio", { name: /English/ }));
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    fireEvent.click(
      screen.getByRole("button", { name: "End session", hidden: true }),
    );
    await act(async () => finish?.(response({ ui_locale: "en" })));
    expect(screen.getByTestId("auth")).toHaveTextContent("guest");
    expect(screen.getByTestId("language")).toHaveTextContent("zh-CN");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
