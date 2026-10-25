#!/usr/bin/env python3
"""Add repeatable discussion cases to the running disposable playground."""

import json
import os
from pathlib import Path
import subprocess
import urllib.request
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
PLAYGROUND = Path(os.environ.get("PLAYGROUND_DIR", ROOT / ".moment-local/playground")).resolve()
ORIGIN = "http://127.0.0.1:18080"
ACTORS = ("xiaoman", "aming", "lena", "xiaobei")


def read(path):
    with urllib.request.urlopen(ORIGIN + path, timeout=10) as response:
        return json.load(response)


def assert_playground(instance, seed, site):
    if (instance.get("environment") != "development"
            or site.get("meta", {}).get("instance_id") != instance.get("instance_id")
            or not instance.get("instance_id")
            or seed.get("plan", {}).get("scenario") != "site-showcase"):
        raise SystemExit("Refusing to write outside the running local site-showcase playground.")


def cli(actor, *args):
    result = subprocess.run(
        [str(PLAYGROUND / "moment"), *args, "--data-dir", str(PLAYGROUND / "data"),
         "--as", actor, "--json", "--no-input"],
        text=True, capture_output=True,
    )
    if result.returncode:
        raise SystemExit(result.stdout.strip() or result.stderr.strip())
    return json.loads(result.stdout)["data"]


class Builder:
    def __init__(self, path, instance_id, runner=cli):
        self.path = path
        self.runner = runner
        self.manifest = {"instance_id": instance_id, "run_id": uuid4().hex,
                         "entities": {}, "cases": [], "deleted": [], "complete": False}
        if path.exists():
            cached = json.loads(path.read_text())
            if cached.get("instance_id") == instance_id:
                self.manifest = cached

    def save(self):
        temporary = self.path.with_suffix(".tmp")
        temporary.write_text(json.dumps(self.manifest, ensure_ascii=False, indent=2) + "\n")
        temporary.chmod(0o600)
        temporary.replace(self.path)

    def entity(self, key, actor, *args):
        if key not in self.manifest["entities"]:
            result = self.runner(actor, *args, "--idempotency-key",
                                 "comment-showcase:" + self.manifest["run_id"] + ":" + key)
            self.manifest["entities"][key] = result
            self.save()
        return self.manifest["entities"][key]

    def case(self, key, post_id, comment_id=None, whole_thread=False, note=""):
        if any(item["key"] == key for item in self.manifest["cases"]):
            return
        path = "/m/" + post_id
        if comment_id:
            path += "/comments/" + comment_id + ("?view=thread" if whole_thread else "")
        self.manifest["cases"].append({"key": key, "post_id": post_id,
                                       "comment_id": comment_id, "path": path, "note": note})
        self.save()

    def delete(self, key, actor, comment_id):
        if key not in self.manifest["deleted"]:
            try:
                self.runner(actor, "comment", "delete", comment_id, "--yes")
            except SystemExit as failure:
                if "COMMENT_NOT_FOUND" not in str(failure):
                    raise
            self.manifest["deleted"].append(key)
            self.save()


def print_cases(manifest):
    print("Comment showcase: " + str(len(manifest["cases"])) + " cases")
    for case in manifest["cases"]:
        print(case["key"] + "\t" + ORIGIN + case["path"])


def add_long_context_case(builder, post_id, root_id):
    child = builder.entity("long-media.reply", "lena", "comment", "reply", post_id,
                           root_id, "--reply-to", root_id,
                           "--content", "完整上级节点验收：上方长正文与图片直接显示，不使用 Show more。")
    builder.case("comments.long-context", post_id, child["id"],
                 note="Full long ancestor with image in the conversation and share card")


def main():
    instance = json.loads((PLAYGROUND / "data/instance.json").read_text())
    seed = json.loads((PLAYGROUND / "seed.json").read_text())["data"]
    assert_playground(instance, seed, read("/api/v1/site"))
    builder = Builder(PLAYGROUND / "comment-showcase.json", instance["instance_id"])
    if builder.manifest["complete"]:
        add_long_context_case(builder, builder.manifest["entities"]["post"]["id"],
                              builder.manifest["entities"]["long-media"]["id"])
        print_cases(builder.manifest)
        return

    post = builder.entity("post", "xiaoman", "post", "create", "--content",
                          "评论区验收：多账号、一级缩进、直接回复、整层讨论与返回位置。")
    post_id = post["id"]
    builder.case("comments.post", post_id, note="27+ floors; scroll to load the next root page")
    for floor, count in enumerate([0, 1, 2, 11, 103] + [0] * 22):
        actor = ACTORS[floor % len(ACTORS)]
        root = builder.entity("floor." + str(floor), actor, "comment", "create", post_id,
                              "--content", "第 " + str(floor + 1) + " 层讨论，" + str(count) + " 条楼内回复。")
        root_id = root["id"]
        if floor < 5:
            builder.case("comments.replies-" + str(count), post_id, root_id,
                         note=str(count) + " replies; focus view")
            builder.case("comments.floor-" + str(count), post_id, root_id, True,
                         note=str(count) + " replies; whole floor")
        first_reply = None
        for index in range(count):
            target_id = first_reply["id"] if first_reply and (index == 1 or index % 2 == 0) else root_id
            reply = builder.entity(
                "floor." + str(floor) + ".reply." + str(index),
                ACTORS[(floor + index + 1) % len(ACTORS)],
                "comment", "reply", post_id, root_id, "--reply-to", target_id,
                "--content", "楼内回复 " + str(index + 1) + "：多账号对话，保留明确的被回复对象。")
            if first_reply is None:
                first_reply = reply
            if floor == 4 and index == 0:
                builder.case("comments.direct-many", post_id, reply["id"],
                             note="More than 50 direct replies to a child comment")
            if floor == 4 and index == 1:
                builder.case("comments.context-deep", post_id, reply["id"],
                             note="Child replying to another child; post and target quotes")
        if floor == 2:
            cli("xiaoman", "comment", "pin", post_id, root_id, "--yes")

    unaddressed = builder.entity("unaddressed.root", "aming", "comment", "create", post_id,
                                "--content", "兼容旧回复对象未记录的讨论")
    old = builder.entity("unaddressed.reply", "lena", "comment", "reply", post_id,
                         unaddressed["id"], "--content", "未记录具体对象的楼内回复。")
    builder.case("comments.unaddressed", post_id, old["id"], note="Neutral label without inventing a target")

    deleted_root = builder.entity("deleted-target.root", "aming", "comment", "create", post_id,
                                  "--content", "目标删除后，其他人的回复仍然留在这一层。")
    target = builder.entity("deleted-target.target", "lena", "comment", "reply", post_id,
                            deleted_root["id"], "--reply-to", deleted_root["id"],
                            "--content", "这条子评论会被删除。")
    survivor = builder.entity("deleted-target.survivor", "xiaobei", "comment", "reply", post_id,
                              deleted_root["id"], "--reply-to", target["id"],
                              "--content", "被回复的评论已删除，这条回复继续存在。")
    builder.delete("target", "lena", target["id"])
    builder.case("comments.deleted-target", post_id, survivor["id"], note="Live child with a deleted target")
    removed = builder.entity("deleted.root", "xiaoman", "comment", "create", post_id,
                             "--content", "这层楼会被删除，深链接应显示不存在。")
    builder.entity("deleted.child", "lena", "comment", "reply", post_id, removed["id"],
                   "--reply-to", removed["id"], "--content", "随主评论级联删除。")
    builder.delete("root", "xiaoman", removed["id"])
    builder.case("comments.deleted", post_id, removed["id"], note="Missing comment deep link")

    source = next(case for case in seed["showcase"]["cases"] if case["key"] == "post.recent.image-one")
    media = read("/api/v1/posts/" + source["id"])["data"]["media"][0]["media_id"]
    media_manifest = PLAYGROUND / "comment-showcase-media.json"
    media_manifest.write_text(json.dumps([{"media_id": media, "alt_text": "评论里的测试图片"}]))
    long_body = ("这条长评论用于检查展开全文、正文选区和返回阅读位置。\n\n" * 25
                 + "相关链接：https://example.com/comment-discussion")
    long_comment = builder.entity("long-media", "xiaoman", "comment", "create", post_id,
                                  "--content", long_body, "--media-manifest", str(media_manifest))
    builder.case("comments.long-media", post_id, long_comment["id"],
                 note="Long body, independent link and managed image")
    add_long_context_case(builder, post_id, long_comment["id"])

    private_post = builder.entity("private.post", "aming", "post", "create", "--visibility", "only_me",
                                  "--content", "只给阿明查看的讨论样本。")
    private_comment = builder.entity("private.comment", "aming", "comment", "create",
                                     private_post["id"], "--content", "访客和其他管理员不能读取这条私密评论。")
    builder.case("comments.private", private_post["id"], private_comment["id"],
                 note="Only aming can read; anonymous and another administrator see missing")

    articles_path = PLAYGROUND / "article-showcase.json"
    if articles_path.exists():
        articles = json.loads(articles_path.read_text())
        if articles.get("instance_id") == instance["instance_id"]:
            article = next((case for case in articles["cases"] if case["key"] == "article.cover"), None)
            if article:
                root = builder.entity("article.root", "lena", "comment", "create", article["id"],
                                      "--content", "文章下面也可以进入评论详情。")
                child = builder.entity("article.reply", "aming", "comment", "reply", article["id"],
                                       root["id"], "--reply-to", root["id"],
                                       "--content", "文章标题、摘要和被回复评论应当正确显示。")
                builder.case("comments.article", article["id"], child["id"], note="Article summary and immediate target")
    builder.manifest["complete"] = True
    builder.save()
    print_cases(builder.manifest)


if __name__ == "__main__":
    main()
