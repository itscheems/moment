import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useRef, useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { PublicApi } from "../../api/public";
import { authApi } from "../../api/auth";
import { Dialog } from "../../components/Dialog";
import { SessionProvider } from "../auth/SessionProvider";
import { makeSiteIdentity } from "../../test/accountFixtures";
import { CommentForm } from "./CommentComposer";

const site = makeSiteIdentity();
const api = new PublicApi();
const created = vi.fn();
const people = [
  {
    userId: "a".repeat(32),
    username: "ada",
    displayName: "Ada",
    avatarMediaId: null,
    profileUrl: "/ada",
  },
  {
    userId: "b".repeat(32),
    username: "birch",
    displayName: "Birch",
    avatarMediaId: null,
    profileUrl: "/birch",
  },
];

beforeEach(() => {
  // jsdom has no layout. Give the positioning library a visible reference;
  // caret alignment, clipping, and collision behavior are browser checks.
  vi.spyOn(document.documentElement, "clientWidth", "get").mockReturnValue(
    1024,
  );
  vi.spyOn(document.documentElement, "clientHeight", "get").mockReturnValue(
    768,
  );
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(1024);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(768);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return this instanceof HTMLTextAreaElement
        ? new DOMRect(40, 100, 600, 80)
        : new DOMRect(0, 0, 1024, 768);
    },
  );
  localStorage.clear();
  sessionStorage.clear();
  window.history.replaceState({}, "", "/");
  vi.spyOn(authApi, "accountSession").mockResolvedValue({
    csrfToken: "test-csrf",
    expiresAt: "2999-01-01T00:00:00Z",
    preferences: { uiLocale: null },
    account: {
      ...site,
      userId: site.profileUserId,
      profileUrl: "/writer",
      role: "member",
      status: "active",
      usernameChosen: true,
    },
    limits: {
      profilePictureBytes: 8_000_000,
      displayNameLength: 80,
      bioLength: 500,
    },
  });
  vi.spyOn(api, "mentionSuggestions").mockResolvedValue(people);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  created.mockClear();
});

function Example({
  modal,
  active = true,
}: {
  modal: boolean;
  active?: boolean;
}) {
  const [content, setContent] = useState("");
  const [open, setOpen] = useState(true);
  const field = useRef<HTMLTextAreaElement>(null);
  const form = (
    <div data-testid="clipped-form" style={{ overflow: "hidden" }}>
      <CommentForm
        api={api}
        active={active}
        commentsEnabled
        content={content}
        setContent={setContent}
        onCreated={created}
        onClosed={() => {}}
        parentId={null}
        postId="01K2ABCDEF0123456789ABCDE0"
        site={site}
        textareaRef={field}
      />
    </div>
  );
  return (
    <NavigationProvider>
      <SessionProvider>
        {modal ? (
          <Dialog.Root open={open} onOpenChange={setOpen}>
            <Dialog.Portal>
              <Dialog.Backdrop />
              <Dialog.Popup initialFocus={field}>
                <Dialog.Title>Reply test</Dialog.Title>
                {form}
              </Dialog.Popup>
            </Dialog.Portal>
          </Dialog.Root>
        ) : (
          form
        )}
      </SessionProvider>
    </NavigationProvider>
  );
}

it.each([false, true])(
  "shares caret suggestions and keyboard insertion in replies (modal=%s)",
  async (modal) => {
    render(<Example modal={modal} />);
    const field = await screen.findByRole("textbox", { name: "回复内容" });
    field.focus();
    fireEvent.change(field, {
      target: { value: "Before @ad", selectionStart: 10 },
    });
    const list = await screen.findByRole("listbox", { name: "提及用户建议" });
    expect(list).toHaveAttribute("data-placement", "caret");
    expect(screen.getByTestId("clipped-form")).not.toContainElement(list);
    expect(field).toHaveAttribute("aria-controls", list.id);
    expect(field).toHaveFocus();
    fireEvent.keyDown(field, { key: "ArrowDown" });
    await waitFor(() =>
      expect(screen.getByRole("option", { name: /@birch/ })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );
    fireEvent.keyDown(field, { key: "Enter" });
    expect(field).toHaveValue("Before @birch ");
    await waitFor(() =>
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
    );
    expect(created).not.toHaveBeenCalled();
    if (modal)
      expect(screen.getByRole("dialog", { name: "Reply test" })).toBeVisible();
  },
);

it("dismisses reply suggestions before the dialog and preserves input", async () => {
  render(<Example modal />);
  const field = await screen.findByRole("textbox", { name: "回复内容" });
  field.focus();
  fireEvent.change(field, { target: { value: "Before @", selectionStart: 8 } });
  await screen.findByRole("listbox");
  fireEvent.keyDown(field, { key: "Escape" });
  await waitFor(() =>
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("dialog", { name: "Reply test" })).toBeVisible();
  expect(field).toHaveValue("Before @");
  fireEvent.keyDown(field, { key: "Escape" });
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("keeps suggestions inside modal ownership when a portaled option is clicked", async () => {
  render(<Example modal />);
  const field = await screen.findByRole("textbox", { name: "回复内容" });
  fireEvent.change(field, { target: { value: "@", selectionStart: 1 } });
  fireEvent.click(await screen.findByRole("option", { name: /@ada/ }));
  expect(field).toHaveValue("@ada ");
  expect(screen.getByRole("dialog", { name: "Reply test" })).toBeVisible();
  await waitFor(() => expect(field).toHaveFocus());
});

it("removes suggestions from an inactive kept-mounted form", async () => {
  const view = render(<Example modal={false} />);
  const field = await screen.findByRole("textbox", { name: "回复内容" });
  fireEvent.change(field, { target: { value: "@", selectionStart: 1 } });
  await screen.findByRole("listbox");
  view.rerender(<Example modal={false} active={false} />);
  await waitFor(() =>
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
  );
  expect(field).toHaveValue("@");
});

it("does not consume composition or modified editing keys as mention picks", async () => {
  render(<Example modal={false} />);
  const field = await screen.findByRole("textbox", { name: "回复内容" });
  field.focus();
  fireEvent.change(field, { target: { value: "@", selectionStart: 1 } });
  await screen.findByRole("listbox");
  expect(fireEvent.keyDown(field, { key: "Enter", isComposing: true })).toBe(
    true,
  );
  expect(fireEvent.keyDown(field, { key: "Enter", shiftKey: true })).toBe(true);
  expect(fireEvent.keyDown(field, { key: "Tab", shiftKey: true })).toBe(true);
  expect(field).toHaveValue("@");
  expect(created).not.toHaveBeenCalled();
});

it("closes suggestions when the caret moves out of an active mention", async () => {
  render(<Example modal={false} />);
  const field = await screen.findByRole<HTMLTextAreaElement>("textbox", {
    name: "回复内容",
  });
  field.focus();
  fireEvent.change(field, { target: { value: "Before @", selectionStart: 8 } });
  await screen.findByRole("listbox");
  field.setSelectionRange(0, 0);
  fireEvent.select(field);
  await waitFor(() =>
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
  );
  expect(field).toHaveValue("Before @");
});

it("dismisses suggestions on field blur without changing the draft", async () => {
  render(<Example modal={false} />);
  const field = await screen.findByRole("textbox", { name: "回复内容" });
  field.focus();
  fireEvent.change(field, { target: { value: "Before @", selectionStart: 8 } });
  await screen.findByRole("listbox");
  fireEvent.blur(field);
  await waitFor(() =>
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
  );
  expect(field).toHaveValue("Before @");
});
