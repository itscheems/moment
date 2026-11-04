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
            if {"article.cover", "article.no-cover", "article.private", "article.draft", "article.empty-draft", "article.withdrawn", "article.map", "article.math"} <= {case["key"] for case in cached["cases"]}:
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
        ("article.map", "旅行的地图，也是一段生活", False, "everyone"),
        ("article.math", "数学公式排版示例", False, "everyone"),
    ]:
        if any(c["key"] == key for c in manifest["cases"]):
            continue
        document = body
        if key == "article.math":
            def formula(latex):
                return {"type": "blockMath", "attrs": {"latex": latex}}

            def heading(text):
                return {"type": "heading", "attrs": {"level": 2}, "content": [{"type": "text", "text": text}]}

            document = {"schema_version": 4, "type": "doc", "content": [
                paragraph("文字和公式可以一起写。这里包含行内表达式、分式、根式、矩阵、分段函数和多行推导，可在编辑器中点击公式修改源码。"),
                heading("行内公式与中文混排"),
                {"type": "paragraph", "content": [
                    {"type": "text", "text": "设向量 "},
                    {"type": "inlineMath", "attrs": {"latex": r"\mathbf{v}=(3,4)"}},
                    {"type": "text", "text": "，它的长度为 "},
                    {"type": "inlineMath", "attrs": {"latex": r"\lVert\mathbf{v}\rVert=\sqrt{3^2+4^2}=5"}},
                    {"type": "text", "text": "。公式与正文处在同一段落里。"},
                ]},
                heading("分式、根式与积分"),
                formula(r"x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}"),
                formula(r"\int_0^1 x^2\,\mathrm{d}x=\left[\frac{x^3}{3}\right]_0^1=\frac{1}{3}"),
                heading("矩阵与分段函数"),
                formula(r"A=\begin{pmatrix}1&2\\3&4\end{pmatrix},\qquad\det(A)=1\cdot4-2\cdot3=-2"),
                formula(r"|x|=\begin{cases}x,&x\geq0\\-x,&x<0\end{cases}"),
                heading("多行推导"),
                formula(r"\begin{aligned}S_n&=1+2+\cdots+n\\2S_n&=(n+1)+\cdots+(n+1)=n(n+1)\\S_n&=\frac{n(n+1)}{2}\end{aligned}"),
                heading("窄屏下的长公式"),
                paragraph("长公式在自身区域内横向滚动，页面与底部导航保持在屏幕范围内。"),
                formula(r"\left(\sum_{i=1}^{n}a_ib_i\right)^2\leq\left(\sum_{i=1}^{n}a_i^2\right)\left(\sum_{i=1}^{n}b_i^2\right),\qquad a_i,b_i\in\mathbb{R},\quad n\in\mathbb{N}_{>0}"),
                heading("代码里的符号保持原样"),
                {"type": "codeBlock", "attrs": {"language": "plaintext"}, "content": [{"type": "text", "text": r"$x^2$ 与 $$x^2$$ 在代码块里只是普通文本。"}]},
                paragraph("验收时还可以测试：插入公式后取消、修改后撤销、保存草稿再重新打开，以及发布后的阅读效果。"),
            ]}
        if key == "article.map":
            document = {"schema_version": 3, "type": "doc", "content": [
                paragraph("在银座走到天黑，第二天再去京都。"),
                {"type": "image", "attrs": {"media_id": media_id, "alt": "旅行里的一张照片"}},
                {"type": "mapCard", "attrs": {"id": "showcase-map", "map_card": {"places": [
                    {"name": "银座", "display_name": "日本东京都中央区银座", "latitude": 35.6717, "longitude": 139.765, "coordinate_system": "wgs84"},
                    {"name": "京都", "display_name": "日本京都府京都市", "latitude": 35.0116, "longitude": 135.7681, "coordinate_system": "wgs84"}], "connect_points": False}}},
                paragraph("地图下面，继续写这一天的故事。") ]}
        args = ["article", "new", "--title", title, "--body", "-"]
        if cover:
            args += ["--cover", media_id]
        draft = next((d for d in existing_drafts if d["title"] == title and cli("article", "draft", d["id"])["body"] == document), None)
        if draft is None:
            draft = cli(*args, body=document)
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
