#!/usr/bin/env python3
"""Generates the gRPC part of the Bruno collection (docs/bruno/grpc) from proto/.

Every rpc becomes one .bru request with a sample body and its docs (method, request and response fields, enums),
taken from the proto comments. The HTTP part (docs/bruno/http) is written by hand and left untouched.

Usage (from lt-edu-api/, needs buf):  python3 scripts/bruno-gen.py
"""

import html
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "docs", "bruno", "grpc")

SCALAR = {
    "TYPE_STRING": "string", "TYPE_BOOL": "bool", "TYPE_INT32": "int32", "TYPE_INT64": "int64",
    "TYPE_UINT32": "uint32", "TYPE_UINT64": "uint64", "TYPE_SINT32": "sint32", "TYPE_SINT64": "sint64",
    "TYPE_FIXED32": "fixed32", "TYPE_FIXED64": "fixed64", "TYPE_SFIXED32": "sfixed32", "TYPE_SFIXED64": "sfixed64",
    "TYPE_DOUBLE": "double", "TYPE_FLOAT": "float", "TYPE_BYTES": "bytes",
}


def load_image():
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "image.json")
        subprocess.run(["buf", "build", "-o", path], cwd=ROOT, check=True)
        with open(path) as f:
            return json.load(f)


class Registry:
    def __init__(self, image):
        self.messages, self.enums, self.comments, self.services = {}, {}, {}, []
        for f in image["file"]:
            pkg = f.get("package", "")
            comments = {}
            for loc in f.get("sourceCodeInfo", {}).get("location", []):
                text = (loc.get("leadingComments", "") + " " + loc.get("trailingComments", "")).strip()
                if text:
                    comments[tuple(loc["path"])] = " ".join(line.strip() for line in text.splitlines() if line.strip())
            for i, m in enumerate(f.get("messageType", [])):
                self._message(pkg, m, (4, i), comments)
            for i, e in enumerate(f.get("enumType", [])):
                self._enum(pkg, e, (5, i), comments)
            for si, s in enumerate(f.get("service", [])):
                methods = []
                for mi, m in enumerate(s.get("method", [])):
                    methods.append({**m, "doc": comments.get((6, si, 2, mi), "")})
                self.services.append({"package": pkg, "name": s["name"], "file": f["name"],
                                      "doc": comments.get((6, si), ""), "methods": methods})

    def _message(self, scope, m, path, comments):
        full = f".{scope}.{m['name']}"
        fields = []
        for fi, fd in enumerate(m.get("field", [])):
            fields.append({**fd, "doc": comments.get(path + (2, fi), "")})
        self.messages[full] = {**m, "full": full, "fields": fields, "doc": comments.get(path, "")}
        for ni, n in enumerate(m.get("nestedType", [])):
            self._message(f"{scope}.{m['name']}", n, path + (3, ni), comments)
        for ei, e in enumerate(m.get("enumType", [])):
            self._enum(f"{scope}.{m['name']}", e, path + (4, ei), comments)

    def _enum(self, scope, e, path, comments):
        full = f".{scope}.{e['name']}"
        values = [{"name": v["name"], "doc": comments.get(path + (2, vi), "")} for vi, v in enumerate(e.get("value", []))]
        self.enums[full] = {"name": e["name"], "full": full, "values": values, "doc": comments.get(path, "")}

    def is_map(self, fd):
        if fd.get("type") != "TYPE_MESSAGE":
            return False
        m = self.messages.get(fd["typeName"])
        return bool(m and m.get("options", {}).get("mapEntry"))

    def type_label(self, fd):
        if self.is_map(fd):
            k, v = self.messages[fd["typeName"]]["fields"]
            return f"map<{self.type_label(k)}, {self.type_label(v)}>"
        t = fd["type"]
        base = SCALAR.get(t) or fd.get("typeName", "").rsplit(".", 1)[-1]
        if fd.get("label") == "LABEL_REPEATED":
            return f"repeated {base}"
        if fd.get("proto3Optional"):
            return f"optional {base}"
        return base

    def sample_scalar(self, fd):
        t, name = fd["type"], fd["name"]
        if t == "TYPE_STRING":
            if name == "institution_slug" or name == "institution":
                return "{{institution}}"
            return ""
        if t == "TYPE_BOOL":
            return False
        if t == "TYPE_BYTES":
            return ""
        if t == "TYPE_ENUM":
            values = self.enums[fd["typeName"]]["values"]
            pick = next((v for v in values if not v["name"].endswith("UNSPECIFIED")), values[0])
            return pick["name"]
        return 0

    def sample(self, full, depth=0, seen=()):
        if full == ".google.protobuf.Timestamp":
            return {"seconds": 0, "nanos": 0}
        m = self.messages[full]
        out, oneofs_used = {}, set()
        for fd in m["fields"]:
            # Only the first member of each real oneof (proto3 optional uses a synthetic one)
            if "oneofIndex" in fd and not fd.get("proto3Optional"):
                if fd["oneofIndex"] in oneofs_used:
                    continue
                oneofs_used.add(fd["oneofIndex"])
            if self.is_map(fd):
                out[fd["name"]] = {}
                continue
            if fd["type"] == "TYPE_MESSAGE":
                if depth >= 3 or fd["typeName"] in seen:
                    value = {}
                else:
                    value = self.sample(fd["typeName"], depth + 1, seen + (full,))
            else:
                value = self.sample_scalar(fd)
            out[fd["name"]] = [value] if fd.get("label") == "LABEL_REPEATED" else value
        return out

    def enums_in(self, full, acc=None, seen=None):
        """Enums reachable from a message (to document their values)."""
        acc = {} if acc is None else acc
        seen = set() if seen is None else seen
        if full in seen or full not in self.messages:
            return acc
        seen.add(full)
        for fd in self.messages[full]["fields"]:
            if fd["type"] == "TYPE_ENUM":
                acc[fd["typeName"]] = self.enums[fd["typeName"]]
            elif fd["type"] == "TYPE_MESSAGE":
                self.enums_in(fd["typeName"], acc, seen)
        return acc

    def messages_in(self, full, acc=None, root=True):
        """Nested message types used by a message (documented under 'Tipos')."""
        acc = {} if acc is None else acc
        if full not in self.messages:
            return acc
        for fd in self.messages[full]["fields"]:
            if fd["type"] != "TYPE_MESSAGE":
                continue
            child = fd["typeName"]
            if self.is_map(fd):
                v = self.messages[child]["fields"][1]
                if v["type"] != "TYPE_MESSAGE":
                    continue
                child = v["typeName"]
            if child in acc or child == full or child not in self.messages:
                continue
            acc[child] = self.messages[child]
            self.messages_in(child, acc, False)
        return acc


def cell(text):
    return (text or "").replace("|", "\\|").replace("{{", "{ {")


def field_table(reg, msg):
    if not msg["fields"]:
        return "_Sin campos._\n"
    rows = ["| Campo | Tipo | Descripción |", "|---|---|---|"]
    for fd in msg["fields"]:
        rows.append(f"| `{fd['name']}` | `{reg.type_label(fd)}` | {cell(fd['doc'])} |")
    return "\n".join(rows) + "\n"


def method_docs(reg, svc, m):
    req, res = reg.messages[m["inputType"]], reg.messages[m["outputType"]]
    lines = [f"# {svc['name']}.{m['name']}", ""]
    if m["doc"]:
        lines += [cell(m["doc"]), ""]
    lines += [f"`/{svc['package']}.{svc['name']}/{m['name']}` · unary · `{svc['file']}`", ""]
    lines += [f"## Request — `{req['name']}`", ""]
    if req["doc"]:
        lines += [cell(req["doc"]), ""]
    lines += [field_table(reg, req)]
    lines += [f"## Response — `{res['name']}`", ""]
    if res["doc"]:
        lines += [cell(res["doc"]), ""]
    lines += [field_table(reg, res)]
    nested = {}
    reg.messages_in(m["inputType"], nested)
    reg.messages_in(m["outputType"], nested)
    nested.pop(".google.protobuf.Timestamp", None)
    if nested:
        lines += ["## Tipos", ""]
        for t in nested.values():
            lines += [f"### `{t['name']}`", ""]
            if t["doc"]:
                lines += [cell(t["doc"]), ""]
            lines += [field_table(reg, t)]
    enums = {}
    reg.enums_in(m["inputType"], enums)
    reg.enums_in(m["outputType"], enums)
    if enums:
        lines += ["## Enums", ""]
        for e in enums.values():
            values = ", ".join(f"`{v['name']}`" + (f" ({cell(v['doc'])})" if v["doc"] else "") for v in e["values"])
            lines += [f"- **{e['name']}**: {values}"]
        lines += [""]
    lines += ["Errores: status gRPC con un `ltedu.common.v1.ErrorDetail` (code, message, fields) en los details."]
    return "\n".join(lines)


def indent(text, n):
    pad = " " * n
    return "\n".join(pad + line if line else "" for line in text.splitlines())


def write_request(path, seq, svc, m, reg):
    body = json.dumps(reg.sample(m["inputType"]), indent=2, ensure_ascii=False)
    content = f"""meta {{
  name: {m['name']}
  type: grpc
  seq: {seq}
}}

grpc {{
  url: {{{{grpcUrl}}}}
  method: /{svc['package']}.{svc['name']}/{m['name']}
  body: grpc
  auth: none
  methodType: unary
}}

metadata {{
  authorization: Bearer {{{{token}}}}
  x-user-agent: bruno
}}

body:grpc {{
  name: message 1
  content: '''
{indent(body, 4)}
  '''
}}

docs {{
{indent(method_docs(reg, svc, m), 2)}
}}
"""
    with open(path, "w") as f:
        f.write(content)


def main():
    reg = Registry(load_image())
    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(OUT)
    with open(os.path.join(OUT, "folder.bru"), "w") as f:
        f.write("meta {\n  name: gRPC\n  seq: 2\n}\n\ndocs {\n"
                "  # gRPC (APP__GRPC__PORT, 5051 en local)\n\n"
                "  Cada servicio de `proto/ltedu` es una carpeta; cada rpc, una petición. Todas llevan\n"
                "  `authorization: Bearer {{token}}` en la metadata (inicia sesión con HTTP › Auth › Sign in).\n"
                "  Generado por `scripts/bruno-gen.py`: no editar a mano.\n}\n")
    total = 0
    services = sorted((s for s in reg.services if s["package"].startswith("ltedu.")), key=lambda s: (s["package"], s["name"]))
    for si, svc in enumerate(services, 1):
        folder = os.path.join(OUT, svc["name"])
        os.makedirs(folder)
        doc = cell(svc["doc"]) or ""
        with open(os.path.join(folder, "folder.bru"), "w") as f:
            f.write(f"meta {{\n  name: {svc['name']}\n  seq: {si}\n}}\n\ndocs {{\n"
                    f"  # {svc['package']}.{svc['name']}\n\n"
                    + (f"  {doc}\n\n" if doc else "")
                    + f"  Definido en `proto/{svc['file']}` · {len(svc['methods'])} métodos.\n}}\n")
        for mi, m in enumerate(svc["methods"], 1):
            write_request(os.path.join(folder, f"{m['name']}.bru"), mi, svc, m, reg)
            total += 1
    print(f"{len(services)} servicios, {total} métodos → {os.path.relpath(OUT, ROOT)}", file=sys.stderr)
    write_html(reg, services)


# ---------------------------------------------------------------------------------------------------------------
# HTML: docs/html/index.html, one static page with the HTTP part (read from docs/bruno/http) and the gRPC part
# ---------------------------------------------------------------------------------------------------------------

HTML_OUT = os.path.join(ROOT, "docs", "html", "index.html")
HTTP_DIR = os.path.join(ROOT, "docs", "bruno", "http")


def esc(text):
    return html.escape(text or "", quote=True)


def inline_md(text):
    out = esc(text)
    out = re.sub(r"`([^`]+)`", r"<code>\1</code>", out)
    out = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", out)
    out = re.sub(r"\[([^\]]+)\]\((https?://[^)]+)\)", r'<a href="\2">\1</a>', out)
    return out


def md_to_html(text):
    """Enough Markdown for the hand-written docs: headings, tables, lists, fenced code, paragraphs."""
    out, lines, i = [], text.splitlines(), 0

    def cells(row):
        parts = re.split(r"(?<!\\)\|", row.strip().strip("|"))
        return [p.strip().replace("\\|", "|") for p in parts]

    while i < len(lines):
        line = lines[i]
        if not line.strip():
            i += 1
        elif line.startswith("```"):
            i += 1
            code = []
            while i < len(lines) and not lines[i].startswith("```"):
                code.append(lines[i])
                i += 1
            i += 1
            out.append(f"<pre><code>{esc(chr(10).join(code))}</code></pre>")
        elif line.startswith("#"):
            level = min(len(line) - len(line.lstrip("#")) + 2, 6)
            out.append(f"<h{level}>{inline_md(line.lstrip('#').strip())}</h{level}>")
            i += 1
        elif line.startswith("|"):
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                if not re.match(r"^\|[\s\-|:]+\|$", lines[i].strip()):
                    rows.append(cells(lines[i]))
                i += 1
            head = "".join(f"<th>{inline_md(c)}</th>" for c in rows[0])
            body = "".join("<tr>" + "".join(f"<td>{inline_md(c)}</td>" for c in r) + "</tr>" for r in rows[1:])
            out.append(f'<div class="table"><table><thead><tr>{head}</tr></thead><tbody>{body}</tbody></table></div>')
        elif re.match(r"^(\s*)[-*] |^\d+\. ", line):
            tag = "ol" if re.match(r"^\d+\. ", line) else "ul"
            items = []
            while i < len(lines) and lines[i].strip():
                item = lines[i]
                if re.match(r"^\s*[-*] |^\d+\. ", item):
                    items.append(re.sub(r"^\s*([-*]|\d+\.) ", "", item))
                else:
                    items[-1] += " " + item.strip()
                i += 1
            out.append(f"<{tag}>" + "".join(f"<li>{inline_md(t)}</li>" for t in items) + f"</{tag}>")
        else:
            para = []
            while i < len(lines) and lines[i].strip() and not re.match(r"^(#|\||```|\s*[-*] |\d+\. )", lines[i]):
                para.append(lines[i].strip())
                i += 1
            out.append(f"<p>{inline_md(' '.join(para))}</p>")
    return "\n".join(out)


def parse_bru(path):
    """Top-level blocks of a .bru file: {name: content without the 2-space indent}."""
    blocks, name, buf = {}, None, []
    for line in open(path).read().splitlines():
        if name is None:
            m = re.match(r"^([a-z:\-]+) [\{\[]$", line)
            if m:
                name, buf = m.group(1), []
        elif line in ("}", "]"):
            blocks[name] = "\n".join(l[2:] if l.startswith("  ") else l for l in buf)
            name = None
        else:
            buf.append(line)
    return blocks


def kv(block):
    out = {}
    for line in (block or "").splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            out[k.strip()] = v.strip()
    return out


def slug(*parts):
    return "-".join(re.sub(r"[^a-z0-9]+", "-", p.lower()).strip("-") for p in parts)


def http_sections():
    sections = []
    folders = []
    for d in sorted(os.listdir(HTTP_DIR)):
        full = os.path.join(HTTP_DIR, d)
        if not os.path.isdir(full):
            continue
        meta = kv(parse_bru(os.path.join(full, "folder.bru")).get("meta"))
        folders.append((int(meta.get("seq", 99)), d, full))
    for _, d, full in sorted(folders):
        folder = parse_bru(os.path.join(full, "folder.bru"))
        reqs = []
        for f in os.listdir(full):
            if f == "folder.bru" or not f.endswith(".bru"):
                continue
            b = parse_bru(os.path.join(full, f))
            meta = kv(b.get("meta"))
            verb = next(k for k in ("get", "post", "put", "patch", "delete") if k in b)
            url = kv(b[verb]).get("url", "")
            path = url.replace("{{baseUrl}}", "/api/v1")
            docs = b.get("docs", "")
            docs = "\n".join(docs.splitlines()[1:]) if docs.startswith("# ") else docs
            curl = [f"curl -X {verb.upper()} \"http://localhost:5050{path.replace('{{institution}}', 'demo-eba')}\""]
            if kv(b[verb]).get("auth") != "none":
                curl.append('-H "Authorization: Bearer $TOKEN"')
            example = None
            if "body:json" in b:
                example = b["body:json"].strip()
                curl.append("-H 'Content-Type: application/json'")
                curl.append("-d '" + json.dumps(json.loads(example.replace("{{", "").replace("}}", ""))) + "'")
            if "body:multipart-form" in b:
                fields = kv(b["body:multipart-form"])
                example = "\n".join(f"{k.lstrip('~')}: {v}" for k, v in fields.items())
                for k, v in fields.items():
                    k = k.lstrip("~")
                    curl.append(f"-F 'files=@archivo.pdf'" if k == "files" else f"-F '{k}={v.replace('{{institution}}', 'demo-eba')}'")
            reqs.append({"seq": int(meta.get("seq", 99)), "name": meta.get("name", f), "verb": verb.upper(), "path": path,
                         "docs": md_to_html(docs), "example": example, "curl": " \\\n  ".join(curl),
                         "id": slug("http", d, meta.get("name", f))})
        reqs.sort(key=lambda r: r["seq"])
        sections.append({"name": d, "id": slug("http", d), "docs": md_to_html(folder.get("docs", "")), "reqs": reqs})
    return sections


def field_table_html(reg, msg):
    if not msg["fields"]:
        return '<p class="muted">Sin campos.</p>'
    rows = "".join(f"<tr><td><code>{esc(fd['name'])}</code></td><td><code>{esc(reg.type_label(fd))}</code></td>"
                   f"<td>{esc(fd['doc'])}</td></tr>" for fd in msg["fields"])
    return ('<div class="table"><table><thead><tr><th>Campo</th><th>Tipo</th><th>Descripción</th></tr></thead>'
            f"<tbody>{rows}</tbody></table></div>")


def grpc_method_html(reg, svc, m):
    req, res = reg.messages[m["inputType"]], reg.messages[m["outputType"]]
    full = f"{svc['package']}.{svc['name']}/{m['name']}"
    body = json.dumps(reg.sample(m["inputType"]), indent=2, ensure_ascii=False)
    grpcurl = ("grpcurl -plaintext \\\n  -H \"authorization: Bearer $TOKEN\" \\\n  -d '"
               + json.dumps(reg.sample(m["inputType"]), ensure_ascii=False).replace("{{institution}}", "demo-eba")
               + f"' \\\n  localhost:5051 {full}")
    parts = []
    if m["doc"]:
        parts.append(f"<p>{esc(m['doc'])}</p>")
    parts.append(f'<p class="muted"><code>/{esc(full)}</code> · <code>proto/{esc(svc["file"])}</code></p>')
    parts.append(f"<h4>Request — <code>{esc(req['name'])}</code></h4>")
    if req["doc"]:
        parts.append(f"<p>{esc(req['doc'])}</p>")
    parts.append(field_table_html(reg, req))
    parts.append(f"<h4>Response — <code>{esc(res['name'])}</code></h4>")
    if res["doc"]:
        parts.append(f"<p>{esc(res['doc'])}</p>")
    parts.append(field_table_html(reg, res))
    nested = {}
    reg.messages_in(m["inputType"], nested)
    reg.messages_in(m["outputType"], nested)
    nested.pop(".google.protobuf.Timestamp", None)
    if nested:
        inner = "".join(f"<h5><code>{esc(t['name'])}</code></h5>" + (f"<p>{esc(t['doc'])}</p>" if t["doc"] else "")
                        + field_table_html(reg, t) for t in nested.values())
        parts.append(f'<details class="types"><summary>Tipos ({len(nested)})</summary>{inner}</details>')
    enums = {}
    reg.enums_in(m["inputType"], enums)
    reg.enums_in(m["outputType"], enums)
    if enums:
        items = "".join(f"<li><strong>{esc(e['name'])}</strong>: "
                        + ", ".join(f"<code>{esc(v['name'])}</code>" + (f" ({esc(v['doc'])})" if v["doc"] else "")
                                    for v in e["values"]) + "</li>" for e in enums.values())
        parts.append(f"<h4>Enums</h4><ul>{items}</ul>")
    parts.append('<div class="examples">'
                 f'<div><h4>Cuerpo de ejemplo</h4><pre><code>{esc(body)}</code></pre></div>'
                 f'<div><h4>grpcurl</h4><pre><code>{esc(grpcurl)}</code></pre></div></div>')
    return "\n".join(parts)


def write_html(reg, services):
    http = http_sections()
    collection = parse_bru(os.path.join(ROOT, "docs", "bruno", "collection.bru")).get("docs", "")
    collection = "\n".join(collection.splitlines()[1:]) if collection.startswith("# ") else collection
    n_http = sum(len(s["reqs"]) for s in http)
    n_grpc = sum(len(s["methods"]) for s in services)

    nav, main = [], []
    nav.append('<li><a href="#intro">Introducción</a></li>')
    nav.append('<li class="group">HTTP <span class="count">' + str(n_http) + "</span></li>")
    for s in http:
        nav.append(f'<li data-section="{s["id"]}"><a href="#{s["id"]}">{esc(s["name"])}</a>'
                   f'<span class="count">{len(s["reqs"])}</span></li>')
        items = []
        for r in s["reqs"]:
            search = esc(f"{r['name']} {r['verb']} {r['path']}".lower())
            items.append(
                f'<details class="op" id="{r["id"]}" data-search="{search}">'
                f'<summary><span class="verb verb-{r["verb"].lower()}">{r["verb"]}</span>'
                f'<code class="path">{esc(r["path"])}</code><span class="name">{esc(r["name"])}</span></summary>'
                f'<div class="op-body">{r["docs"]}'
                + (f'<div class="examples"><div><h4>Cuerpo de ejemplo</h4><pre><code>{esc(r["example"])}</code></pre></div>'
                   if r["example"] else '<div class="examples">')
                + f'<div><h4>curl</h4><pre><code>{esc(r["curl"])}</code></pre></div></div></div></details>')
        main.append(f'<section id="{s["id"]}" class="svc"><h2>{esc(s["name"])} <span class="tag">HTTP</span></h2>'
                    f'{s["docs"]}{"".join(items)}</section>')
    nav.append('<li class="group">gRPC <span class="count">' + str(n_grpc) + "</span></li>")
    for svc in services:
        sid = slug("grpc", svc["name"])
        nav.append(f'<li data-section="{sid}"><a href="#{sid}">{esc(svc["name"])}</a>'
                   f'<span class="count">{len(svc["methods"])}</span></li>')
        items = []
        for m in svc["methods"]:
            search = esc(f"{svc['name']} {m['name']} {m['doc']}".lower())
            items.append(
                f'<details class="op" id="{slug(svc["name"], m["name"])}" data-search="{search}">'
                f'<summary><span class="verb verb-grpc">RPC</span><code class="path">{esc(m["name"])}</code>'
                f'<span class="name">{esc(m["doc"][:110] + ("…" if len(m["doc"]) > 110 else ""))}</span></summary>'
                f'<div class="op-body">{grpc_method_html(reg, svc, m)}</div></details>')
        doc = f"<p>{esc(svc['doc'])}</p>" if svc["doc"] else ""
        main.append(f'<section id="{sid}" class="svc"><h2>{esc(svc["name"])} <span class="tag">gRPC</span></h2>'
                    f'<p class="muted"><code>{esc(svc["package"])}</code> · <code>proto/{esc(svc["file"])}</code></p>'
                    f'{doc}{"".join(items)}</section>')

    page = HTML_TEMPLATE.replace("%NAV%", "\n".join(nav)).replace("%MAIN%", "\n".join(main)) \
        .replace("%INTRO%", md_to_html(collection)).replace("%NHTTP%", str(n_http)).replace("%NGRPC%", str(n_grpc))
    os.makedirs(os.path.dirname(HTML_OUT), exist_ok=True)
    with open(HTML_OUT, "w") as f:
        f.write(page)
    print(f"HTML: {n_http} HTTP + {n_grpc} gRPC → {os.path.relpath(HTML_OUT, ROOT)}", file=sys.stderr)


HTML_TEMPLATE = """<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>ListerCore Edu API</title>
<style>
:root {
  color-scheme: light dark;
  --bg-l: #fbfbfa; --bg-d: #15171a;
  --panel-l: #ffffff; --panel-d: #1d2024;
  --text-l: #1b1d21; --text-d: #e6e7e9;
  --muted-l: #5d636d; --muted-d: #9aa1ab;
  --line-l: #e3e4e6; --line-d: #2e3238;
  --code-l: #f1f2f4; --code-d: #262a30;
  --accent-l: #1f5fbf; --accent-d: #7fb0ff;
  --bg: var(--bg-l); --panel: var(--panel-l); --text: var(--text-l); --muted: var(--muted-l);
  --line: var(--line-l); --code: var(--code-l); --accent: var(--accent-l);
  @media (prefers-color-scheme: dark) {
    --bg: var(--bg-d); --panel: var(--panel-d); --text: var(--text-d); --muted: var(--muted-d);
    --line: var(--line-d); --code: var(--code-d); --accent: var(--accent-d);
  }
  @supports (color: light-dark(white, black)) {
    --bg: light-dark(var(--bg-l), var(--bg-d)); --panel: light-dark(var(--panel-l), var(--panel-d));
    --text: light-dark(var(--text-l), var(--text-d)); --muted: light-dark(var(--muted-l), var(--muted-d));
    --line: light-dark(var(--line-l), var(--line-d)); --code: light-dark(var(--code-l), var(--code-d));
    --accent: light-dark(var(--accent-l), var(--accent-d));
  }
  accent-color: var(--accent);
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text);
  font: 15px/1.55 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }
code, pre { font: 13px/1.5 ui-monospace, "SF Mono", Menlo, Consolas, monospace; }
a { color: var(--accent); }
header { position: sticky; top: 0; z-index: 2; display: flex; gap: 1rem; align-items: center;
  padding: .75rem 1.25rem; background: var(--panel); border-bottom: 1px solid var(--line); }
header h1 { font-size: 1rem; margin: 0; white-space: nowrap; }
header .stats { color: var(--muted); font-size: .85rem; white-space: nowrap; }
header input { flex: 1; max-width: 28rem; margin-left: auto; padding: .45rem .7rem; font: inherit;
  color: inherit; background: var(--bg); border: 1px solid var(--line); border-radius: 6px; }
.layout { display: grid; grid-template-columns: 15.5rem minmax(0, 1fr); }
nav { position: sticky; top: 3.3rem; height: calc(100dvh - 3.3rem); overflow-y: auto;
  padding: 1rem .5rem 2rem 1rem; border-right: 1px solid var(--line); }
nav ul { list-style: none; margin: 0; padding: 0; }
nav li { display: flex; justify-content: space-between; align-items: baseline; font-size: .9rem; }
nav li a { color: var(--text); text-decoration: none; padding: .15rem 0; }
nav li a:hover { color: var(--accent); }
nav li.group { margin-top: 1rem; font-weight: 600; text-transform: uppercase; font-size: .75rem;
  letter-spacing: .05em; color: var(--muted); }
nav li[hidden] { display: none; }
.count { color: var(--muted); font-size: .75rem; font-variant-numeric: tabular-nums; }
main { padding: 1.5rem 2rem 4rem; max-width: 72rem; }
section { margin-bottom: 2.5rem; scroll-margin-top: 4rem; }
section[hidden] { display: none; }
h2 { font-size: 1.35rem; margin: 0 0 .5rem; display: flex; gap: .5rem; align-items: center; }
h4 { font-size: .9rem; margin: 1.25rem 0 .4rem; }
h5 { font-size: .85rem; margin: 1rem 0 .3rem; }
.tag { font-size: .7rem; font-weight: 600; padding: .1rem .4rem; border-radius: 4px;
  background: var(--code); color: var(--muted); }
.muted { color: var(--muted); }
p code, li code, td code { background: var(--code); padding: .05rem .3rem; border-radius: 4px; }
pre { background: var(--code); padding: .75rem 1rem; border-radius: 6px; overflow-x: auto; margin: 0; }
.table { overflow-x: auto; }
table { border-collapse: collapse; width: 100%; font-size: .875rem; }
th, td { text-align: left; vertical-align: top; padding: .35rem .6rem; border-bottom: 1px solid var(--line); }
th { color: var(--muted); font-weight: 600; }
td:first-child, td:nth-child(2) { white-space: nowrap; }
details.op { background: var(--panel); border: 1px solid var(--line); border-radius: 8px; margin: .4rem 0;
  scroll-margin-top: 4rem; }
details.op[hidden] { display: none; }
details.op > summary { display: flex; gap: .75rem; align-items: baseline; padding: .55rem .8rem; cursor: pointer;
  list-style: none; }
details.op > summary::-webkit-details-marker { display: none; }
details.op > summary .name { color: var(--muted); font-size: .875rem; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; min-width: 0; }
details.op > summary .path { font-weight: 600; white-space: nowrap; }
details.op[open] > summary { border-bottom: 1px solid var(--line); }
.op-body { padding: .25rem 1rem 1rem; }
.verb { flex: none; width: 3.6rem; text-align: center; font: 600 .7rem/1.6 ui-monospace, Menlo, monospace;
  border-radius: 4px; color: #fff; }
.verb-get { background: #1d7a4f; } .verb-post { background: #1f5fbf; } .verb-put { background: #9a6200; }
.verb-patch { background: #7a4fbf; } .verb-delete { background: #b3261e; } .verb-grpc { background: #4b5563; }
.examples { display: grid; grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr)); gap: 1rem; }
details.types { margin-top: 1rem; }
details.types > summary { cursor: pointer; font-weight: 600; font-size: .9rem; }
#empty { display: none; color: var(--muted); }
@media (max-width: 50rem) {
  .layout { grid-template-columns: 1fr; }
  nav { display: none; }
  main { padding: 1rem; }
  header .stats { display: none; }
}
</style>
</head>
<body>
<header>
  <h1>ListerCore Edu API</h1>
  <span class="stats">%NHTTP% HTTP · %NGRPC% gRPC</span>
  <input type="search" id="filter" placeholder="Filtrar métodos (p. ej. enrollment, ListStudents, sign-in)…" aria-label="Filtrar métodos">
</header>
<div class="layout">
<nav aria-label="Secciones"><ul>
%NAV%
</ul></nav>
<main>
<section id="intro">
<h2>Introducción</h2>
%INTRO%
<p class="muted">Generado por <code>scripts/bruno-gen.py</code> desde <code>proto/</code> y <code>docs/bruno/http</code>.
La misma documentación, ejecutable, está en la colección de Bruno (<code>docs/bruno</code>).</p>
</section>
%MAIN%
<p id="empty">Ningún método coincide con el filtro.</p>
</main>
</div>
<script>
const input = document.getElementById("filter");
const sections = [...document.querySelectorAll("section.svc")];
const navItems = new Map([...document.querySelectorAll("nav li[data-section]")].map((li) => [li.dataset.section, li]));
function applyFilter() {
  const terms = input.value.trim().toLowerCase().split(/\\s+/).filter(Boolean);
  let any = false;
  for (const section of sections) {
    let visible = 0;
    for (const op of section.querySelectorAll("details.op")) {
      const match = terms.every((t) => op.dataset.search.includes(t));
      op.hidden = !match;
      if (match) visible++;
    }
    section.hidden = terms.length > 0 && visible === 0;
    navItems.get(section.id).hidden = section.hidden;
    any ||= visible > 0;
  }
  document.getElementById("intro").hidden = terms.length > 0;
  document.getElementById("empty").style.display = any ? "none" : "block";
}
input.addEventListener("input", applyFilter);
// Deep links open the method they point at
function openTarget() {
  const target = location.hash && document.getElementById(decodeURIComponent(location.hash.slice(1)));
  if (target instanceof HTMLDetailsElement) target.open = true;
}
addEventListener("hashchange", openTarget);
openTarget();
</script>
</body>
</html>
"""


if __name__ == "__main__":
    main()
