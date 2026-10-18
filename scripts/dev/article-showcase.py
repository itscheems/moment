#!/usr/bin/env python3
"""Add named article cases to the running, disposable site-showcase instance."""

import json
import os
from pathlib import Path
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PLAYGROUND = Path(os.environ.get("PLAYGROUND_DIR", ROOT / ".moment-local/playground")).resolve()
ORIGIN = "http://127.0.0.1:18080"


def read(path):
    with urllib.request.urlopen(ORIGIN + path, timeout=10) as response:
        return json.load(response)


def cli(*args, body=None):
    result = subprocess.run(
        [str(PLAYGROUND / "moment"), *args, "--url", ORIGIN,
         "--token-file", str(PLAYGROUND / "account.token"), "--json", "--no-input"],
        input=None if body is None else json.dumps(body, ensure_ascii=False),
        text=True, capture_output=True,
    )
    if result.returncode:
        raise SystemExit(result.stdout.strip() or result.stderr.strip())
    return json.loads(result.stdout)["data"]


def paragraph(text):
    return {"type": "paragraph", "content": [{"type": "text", "text": text}]}


def main():
    # Fixed origin plus identity check: a real site listening on this port is
    # still not a playground. No override can redirect these writes to it.
    instance = json.loads((PLAYGROUND / "data/instance.json").read_text())
    seed = json.loads((PLAYGROUND / "seed.json").read_text())["data"]
    site = read("/api/v1/site")
    if (instance["environment"] != "development"
            or site["meta"]["instance_id"] != instance["instance_id"]
            or seed["plan"]["scenario"] != "site-showcase"):
        raise SystemExit("Refusing to seed an instance other than the local playground.")
    manifest_path = PLAYGROUND / "article-showcase.json"
    manifest = {"instance_id": instance["instance_id"], "cases": []}
    if manifest_path.exists():
        cached = json.loads(manifest_path.read_text())
        if cached["instance_id"] == instance["instance_id"]:
            retained = []
            for case in cached["cases"]:
                try:
                    if case["kind"] == "draft":
                        cli("article", "draft", case["id"])
                    else:
                        cli("post", "get", case["id"])
                except SystemExit as failure:
                    # A case may have been consumed through the editor. Recreate
                    # only that missing case; never discard the existing site.
                    if "ARTICLE_DRAFT_NOT_FOUND" not in str(failure) and "POST_NOT_FOUND" not in str(failure):
                        raise
                    continue
                retained.append(case)
            cached["cases"] = retained
            manifest = cached
            if {"article.cover", "article.no-cover", "article.private", "article.draft", "article.empty-draft", "article.withdrawn"} <= {case["key"] for case in cached["cases"]}:
                print(json.dumps(cached, ensure_ascii=False, indent=2))
                return

    case = next(c for c in seed["showcase"]["cases"] if c["key"] == "post.calendar.y5-dec")
    media_id = read("/api/v1/posts/" + case["id"])["data"]["media"][0]["media_id"]
    marked = []
    for mark, label in [("bold", "加粗"), ("italic", "斜体"), ("strike", "删除线"),
                        ("underline", "下划线"), ("code", "inline code")]:
        marked.extend([{"type": "text", "text": label, "marks": [{"type": mark}]},
                       {"type": "text", "text": " · "}])
    marked.append({"type": "text", "text": "参考链接", "marks": [{"type": "link", "attrs": {"href": "https://example.com/river"}}]})
    marked.extend([{"type": "hardBreak"}, {"type": "text", "text": "换一行，继续记录。"}])
    body = {"schema_version": 1, "type": "doc", "content": [
        {"type": "heading", "attrs": {"level": 1}, "content": [{"type": "text", "text": "沿着河流走一段路"}]},
        {"type": "paragraph", "content": marked},
        {"type": "heading", "attrs": {"level": 2}, "content": [{"type": "text", "text": "上游 · 观察"}]},
        {"type": "blockquote", "content": [paragraph("水面映着树影，慢一点就能看到细节。") ]},
        {"type": "bulletList", "content": [{"type": "listItem", "content": [paragraph("记下天气、光线和水色")]}, {"type": "listItem", "content": [paragraph("同一个地方，不同的季节")]}]},
        {"type": "orderedList", "attrs": {"start": 3}, "content": [{"type": "listItem", "content": [paragraph("沿着堤岸往回走")]}]},
        {"type": "heading", "attrs": {"level": 3}, "content": [{"type": "text", "text": "一小段代码"}]},
        {"type": "codeBlock", "attrs": {"language": "go"}, "content": [{"type": "text", "text": 'fmt.Println("hello, river")'}]},
        {"type": "horizontalRule"},
        {"type": "table", "content": [
            {"type": "tableRow", "content": [{"type": "tableHeader", "content": [paragraph("年份")]}, {"type": "tableHeader", "content": [paragraph("记录")]}]},
            {"type": "tableRow", "content": [{"type": "tableCell", "content": [paragraph("2026")]}, {"type": "tableCell", "content": [paragraph("走到入海口")]}]},
        ]},
        {"type": "image", "attrs": {"media_id": media_id, "alt": "河边的光影"}},
        paragraph("这一段路可以走很多遍，每一遍都有新的发现。"),
    ]}
    existing_drafts = cli("article", "drafts")
    for key, title, cover, visibility in [
        ("article.cover", "一条河流的十年", True, "everyone"),
        ("article.no-cover", "没有封面，也值得记下来", False, "everyone"),
        ("article.private", "只留给自己的河边笔记", False, "only_me"),
        ("article.draft", "写到一半的文章：很长的中文标题在手机上也应该完整显示，继续写下去", False, None),
    ]:
        if any(c["key"] == key for c in manifest["cases"]):
            continue
        args = ["article", "new", "--title", title, "--body", "-"]
        if cover:
            args += ["--cover", media_id]
        draft = next((d for d in existing_drafts if d["title"] == title and cli("article", "draft", d["id"])["body"] == body), None)
        if draft is None:
            draft = cli(*args, body=body)
        item = draft if visibility is None else cli("article", "publish", draft["id"], "--visibility", visibility, "--idempotency-key", key)
        kind = "draft" if visibility is None else "article"
        path = ("/compose/articles/drafts/" if kind == "draft" else "/m/") + item["id"]
        manifest["cases"].append({"key": key, "id": item["id"], "kind": kind, "path": path})
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    if not any(case["key"] == "article.empty-draft" for case in manifest["cases"]):
        empty = {"schema_version": 1, "type": "doc", "content": []}
        draft = cli("article", "new", "--body", "-", "--idempotency-key", "article.empty-draft", body=empty)
        manifest["cases"].append({"key": "article.empty-draft", "id": draft["id"], "kind": "draft", "path": "/compose/articles/drafts/" + draft["id"]})
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    if not any(case["key"] == "article.withdrawn" for case in manifest["cases"]):
        draft = cli("article", "new", "--title", "取消发布后继续写的文章", "--body", "-", "--idempotency-key", "article.withdrawn.draft", body=body)
        post = cli("article", "publish", draft["id"], "--idempotency-key", "article.withdrawn.publish")
        withdrawn = cli("article", "unpublish", post["id"], "--yes")
        manifest["cases"].append({"key": "article.withdrawn", "id": withdrawn["id"], "article_id": post["id"], "kind": "draft", "path": "/compose/articles/drafts/" + withdrawn["id"]})
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(manifest, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
