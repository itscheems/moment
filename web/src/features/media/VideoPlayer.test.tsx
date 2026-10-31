import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { VideoPlayer } from "./VideoPlayer";

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function video() {
  const v = screen.getByLabelText("Clip") as HTMLVideoElement;
  Object.defineProperty(v, "duration", { configurable: true, value: 30 });
  fireEvent.loadedMetadata(v);
  return v;
}
describe("video player interactions", () => {
  it("can request playback before metadata or the complete video has loaded", () => {
    render(
      <VideoPlayer
        src="/clip.mp4"
        poster="/poster.jpg"
        label="Clip"
        onError={vi.fn()}
      />,
    );
    const v = screen.getByLabelText("Clip") as HTMLVideoElement;
    expect(v.preload).toBe("metadata");
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "播放视频" }));
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalledOnce();
    expect(v.preload).toBe("auto");
    expect(screen.getByLabelText("视频正在加载")).toBeInTheDocument();

    fireEvent.canPlay(v);
    fireEvent.playing(v);
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "视频播放器" })).toHaveAttribute(
      "data-playback-state",
      "playing",
    );
  });

  it("does not show playback buffering for an idle or paused preload", () => {
    render(<VideoPlayer src="/clip.mp4" label="Clip" onError={vi.fn()} />);
    const v = video();
    fireEvent.waiting(v);
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "播放视频" }),
    ).toBeInTheDocument();

    fireEvent.playing(v);
    fireEvent.pause(v);
    fireEvent.waiting(v);
    expect(screen.getByRole("group", { name: "视频播放器" })).toHaveAttribute(
      "data-playback-state",
      "paused",
    );
  });

  it("shows actual playback buffering and clears it when playback resumes", () => {
    render(<VideoPlayer src="/clip.mp4" label="Clip" onError={vi.fn()} />);
    const v = video();
    Object.defineProperty(v, "paused", { configurable: true, value: false });
    fireEvent.playing(v);
    fireEvent.waiting(v);
    expect(screen.getByLabelText("视频正在加载")).toBeInTheDocument();

    fireEvent.canPlay(v);
    fireEvent.playing(v);
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "视频播放器" })).toHaveAttribute(
      "data-playback-state",
      "playing",
    );
  });

  it("keeps a pending play request buffering when metadata arrives first", () => {
    render(<VideoPlayer src="/clip.mp4" label="Clip" onError={vi.fn()} />);
    const v = screen.getByLabelText("Clip") as HTMLVideoElement;
    fireEvent.click(screen.getByRole("group", { name: "视频播放器" }));
    Object.defineProperty(v, "paused", { configurable: true, value: false });
    fireEvent.loadedMetadata(v);
    expect(screen.getByLabelText("视频正在加载")).toBeInTheDocument();
    fireEvent.playing(v);
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();
  });

  it("allows immediate playback when an offscreen video is attached again", () => {
    const props = { src: "/clip.mp4", label: "Clip", onError: vi.fn() };
    const { rerender } = render(<VideoPlayer {...props} attached={false} />);
    rerender(<VideoPlayer {...props} attached />);
    expect(
      screen.getByRole("button", { name: "播放视频" }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("视频正在加载")).not.toBeInTheDocument();
  });

  it("offers manual playback when autoplay is rejected without reporting a failed media", async () => {
    vi.mocked(HTMLMediaElement.prototype.play).mockRejectedValue(
      new DOMException("blocked", "NotAllowedError"),
    );
    const failed = vi.fn();
    render(
      <VideoPlayer src="/clip.mp4" label="Clip" autoplay onError={failed} />,
    );
    video();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "播放视频" }),
      ).toBeInTheDocument(),
    );
    expect(failed).not.toHaveBeenCalled();
  });
  it("keeps seek keys and input changes inside the player", () => {
    const parent = vi.fn(),
      snapshot = vi.fn();
    render(
      <div onKeyDown={parent}>
        <VideoPlayer
          src="/clip.mp4"
          label="Clip"
          onError={vi.fn()}
          onPlayback={snapshot}
        />
      </div>,
    );
    const v = video();
    fireEvent.change(screen.getByRole("slider", { name: "播放进度" }), {
      target: { value: "12" },
    });
    expect(v.currentTime).toBe(12);
    fireEvent.keyDown(screen.getByRole("group", { name: "视频播放器" }), {
      key: "ArrowRight",
    });
    expect(v.currentTime).toBe(17);
    expect(parent).not.toHaveBeenCalled();
    fireEvent.timeUpdate(v);
    expect(snapshot).toHaveBeenLastCalledWith(
      expect.objectContaining({ time: 17 }),
    );
  });
  it("opens a mixed-media surface without also starting inline playback", () => {
    const open = vi.fn();
    render(
      <VideoPlayer
        src="/clip.mp4"
        label="Clip"
        onError={vi.fn()}
        onOpen={open}
        openOnSurface
      />,
    );
    video();
    fireEvent.click(screen.getByRole("button", { name: "播放视频" }));
    expect(open).toHaveBeenCalledOnce();
    expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
  });
  it("restores an audible volume when unmuting a zero-volume player", () => {
    render(<VideoPlayer src="/clip.mp4" label="Clip" onError={vi.fn()} />);
    const v = video();
    fireEvent.change(
      screen.getByRole("slider", { name: "音量", hidden: true }),
      { target: { value: "0" } },
    );
    fireEvent.volumeChange(v);
    fireEvent.click(screen.getByRole("button", { name: "取消静音" }));
    expect(v.volume).toBe(1);
    expect(v.muted).toBe(false);
  });
  it("switches only between available sources and falls back from an unsupported original", async () => {
    const failed = vi.fn();
    render(
      <VideoPlayer
        src="/preview.mp4"
        originalSrc="/original.mov"
        label="Clip"
        onError={failed}
      />,
    );
    const v = video();
    v.currentTime = 9;
    fireEvent.click(screen.getByRole("button", { name: "视频设置" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /视频质量/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "返回" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /视频质量/ }));
    fireEvent.click(await screen.findByRole("radio", { name: "原画质" }));
    expect(v).toHaveAttribute("src", "/original.mov");
    expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
    fireEvent.loadedMetadata(v);
    expect(v.currentTime).toBe(9);
    expect(v).not.toHaveAttribute("autoplay");
    fireEvent.error(v);
    expect(v).toHaveAttribute("src", "/preview.mp4");
    expect(failed).not.toHaveBeenCalled();
  });
  it("restores a saved video position and settings after metadata loads", () => {
    render(
      <VideoPlayer
        src="/clip.mp4"
        label="Clip"
        onError={vi.fn()}
        initialPlayback={{ time: 8, muted: true, volume: 0.3, rate: 1.5 }}
      />,
    );
    const v = video();
    expect(v.currentTime).toBe(8);
    expect(v.muted).toBe(true);
    expect(v.volume).toBe(0.3);
    expect(v.playbackRate).toBe(1.5);
  });
});
