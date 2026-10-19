import {
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type FormEvent,
  type RefObject,
} from "react";
import { useStateHistory } from "@mantine/hooks";

interface Snapshot<T> {
  value: T;
  signature: string;
  group: string;
  caret: [number, number] | null;
}

/** One history for text and attachments, shared by the plain-text editors. */
export function useEditHistory<T>({
  value,
  signature,
  group,
  enabled = true,
  input,
  restore,
}: {
  value: T;
  signature: string;
  group: string;
  enabled?: boolean;
  input: RefObject<HTMLTextAreaElement | null>;
  restore: (value: T) => void;
}) {
  const latest = useRef({ value, signature, group, restore });
  latest.current = { value, signature, group, restore };
  const initial: Snapshot<T> = { value, signature, group, caret: null };
  const [entry, actions, state] = useStateHistory<{ current: Snapshot<T> }>({
    current: initial,
  });
  const observed = useRef(signature);
  const typing = useRef({ group: "", at: 0 });
  const composing = useRef(false);
  const paused = useRef(false);
  const resetRequested = useRef(false);
  const [resetVersion, setResetVersion] = useState(0);
  const capture = () => {
    const next = latest.current;
    const field = input.current;
    return {
      value: next.value,
      signature: next.signature,
      group: next.group,
      caret: field
        ? ([field.selectionStart, field.selectionEnd] as [number, number])
        : null,
    };
  };
  const record = () => {
    if (composing.current || observed.current === latest.current.signature)
      return;
    const next = capture();
    const now = Date.now();
    const textOnly = entry.current.group === next.group;
    if (!textOnly) entry.current.caret = next.caret;
    if (
      typing.current.group === next.group &&
      typing.current.at > 0 &&
      now - typing.current.at < 500 &&
      state.current === state.history.length - 1
    ) {
      // Each typing group owns a ref; updating it does not mutate a saved group.
      entry.current = next;
    } else actions.set({ current: next });
    observed.current = next.signature;
    typing.current = { group: next.group, at: textOnly ? now : 0 };
  };
  useLayoutEffect(() => {
    if (resetRequested.current) {
      resetRequested.current = false;
      observed.current = signature;
      typing.current = { group: "", at: 0 };
      actions.reset();
      return;
    }
    if (!enabled) {
      paused.current = true;
      return;
    }
    if (paused.current) {
      paused.current = false;
      if (observed.current !== signature) {
        actions.reset();
        observed.current = signature;
        typing.current = { group: "", at: 0 };
        return;
      }
    }
    record();
  }, [signature, enabled, resetVersion]);
  const travel = (redo: boolean) => {
    if (!enabled || composing.current) return;
    const next = state.history[state.current + (redo ? 1 : -1)]?.current;
    if (!next) return;
    observed.current = next.signature;
    typing.current = { group: "", at: 0 };
    latest.current.restore(next.value);
    if (redo) actions.forward();
    else actions.back();
    window.requestAnimationFrame(() => {
      input.current?.focus();
      if (next.caret) input.current?.setSelectionRange(...next.caret);
    });
  };
  const owns = (target: EventTarget | null) =>
    !(target instanceof Element) ||
    !target.closest('input, textarea, [contenteditable="true"]') ||
    target === input.current;
  return {
    reset: () => {
      resetRequested.current = true;
      setResetVersion((version) => version + 1);
    },
    onKeyDownCapture: (event: KeyboardEvent<HTMLElement>) => {
      if (
        !enabled ||
        event.nativeEvent.isComposing ||
        composing.current ||
        !owns(event.target) ||
        event.altKey ||
        !(event.metaKey || event.ctrlKey)
      )
        return;
      const key = event.key.toLowerCase();
      if (key !== "z" && !(event.ctrlKey && key === "y")) return;
      const dialog =
        event.target instanceof Element
          ? event.target.closest('[role="dialog"]')
          : null;
      if (
        dialog &&
        !dialog.contains(event.currentTarget) &&
        !dialog.matches("[data-map-fullscreen][data-map-editable]")
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      travel(key === "y" || event.shiftKey);
    },
    onBeforeInputCapture: (event: FormEvent<HTMLElement>) => {
      const type = (event.nativeEvent as InputEvent).inputType;
      const field = input.current;
      if (!field || event.target !== field) return;
      if (type !== "historyUndo" && type !== "historyRedo") {
        if (
          !composing.current &&
          entry.current.signature === latest.current.signature
        )
          entry.current.caret = [field.selectionStart, field.selectionEnd];
        return;
      }
      event.preventDefault();
      travel(type === "historyRedo");
    },
    onCompositionStartCapture: () => {
      const field = input.current;
      if (field)
        entry.current.caret = [field.selectionStart, field.selectionEnd];
      composing.current = true;
    },
    onCompositionEndCapture: () => {
      composing.current = false;
      record();
      window.requestAnimationFrame(record);
    },
  };
}
