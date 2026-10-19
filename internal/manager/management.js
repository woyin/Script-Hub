"use strict";
// Native API adapter for the Sub-Store-style interface. No resource or key is persisted in the browser.
const $ = (id) => document.getElementById(id);
const base = location.pathname.replace(/\/+$/, "");
const targets = [
  ["clash", "Clash YAML"],
  ["shadowrocket", "Shadowrocket"],
  ["loon", "Loon"],
  ["egern", "Egern"],
  ["uri", "节点 URI"],
];
const state = {
  page: "subscriptions",
  rows: [],
  query: "",
  sourceFilter: "all",
  search: false,
  folded: false,
  editor: null,
  dirty: false,
  output: null,
  generation: 0,
};
const preferences = {
  theme: matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
  simple: false,
  dual: false,
};
try {
  const saved = JSON.parse(
    localStorage.getItem("scripthub.appearance") || "{}",
  );
  for (const k of Object.keys(preferences))
    if (typeof saved[k] === typeof preferences[k]) preferences[k] = saved[k];
} catch {}
if (!["light", "dark"].includes(preferences.theme)) preferences.theme = "light";
document.documentElement.dataset.theme = preferences.theme;
let toastTimer,
  confirmResolve,
  dialogGeneration = 0;
const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const icon = (name, cls = "icon") =>
  `<svg class="${cls}" aria-hidden="true"><use href="#${cls === "artifact-icon" ? "a-" : "i-"}${name}"/></svg>`;
const art = (kind) =>
  icon(kind === "files" ? "file" : "subscription", "artifact-icon");
function button(label, name, action, extra = "") {
  return `<button class="icon-button ${extra}" type="button" aria-label="${esc(label)}" title="${esc(label)}" data-action="${esc(action)}">${icon(name)}</button>`;
}
function notify(message, error = false) {
  clearTimeout(toastTimer);
  $("message").textContent = message;
  $("message").classList.remove("hidden");
  $("message").classList.toggle("error", error);
  toastTimer = setTimeout(
    () => $("message").classList.add("hidden"),
    error ? 9000 : 3500,
  );
}
function run(fn) {
  return async (event) => {
    try {
      await fn(event);
    } catch (e) {
      if (e.name !== "AbortError") notify(e.message || "操作失败", true);
    }
  };
}
async function responseError(r) {
  let msg = await r.text();
  try {
    const j = JSON.parse(msg);
    msg =
      typeof j.error === "string"
        ? j.error
        : j.error?.message || j.message || msg;
  } catch {}
  return new Error(msg || `请求失败 (${r.status})`);
}
async function api(path, method = "GET", body) {
  const options = {
    method,
    headers: { "Content-Type": "application/json" },
    cache: "no-store",
    referrerPolicy: "no-referrer",
  };
  if (body !== undefined) options.body = JSON.stringify(body);
  const r = await fetch(base + "/api/" + path, options);
  if (!r.ok) throw await responseError(r);
  return r.status === 204 ? null : r.json();
}
function persistAppearance() {
  try {
    localStorage.setItem("scripthub.appearance", JSON.stringify(preferences));
  } catch {}
  document.documentElement.dataset.theme = preferences.theme;
}
function title() {
  if (state.editor)
    return (
      (state.editor.id ? "编辑" : "添加") +
      (state.editor.kind === "files" ? "文件" : "订阅")
    );
  return {
    subscriptions: "订阅",
    files: "文件",
    shares: "分享",
    profile: "我的",
  }[state.page];
}
function navigation() {
  const items = [
    ["subscriptions", "link", "订阅"],
    ["files", "files", "文件"],
    ["shares", "share", "分享"],
    ["profile", "settings", "我的"],
  ];
  const html = items
    .map(
      ([page, i, name]) =>
        `<button class="nav-item ${state.page === page ? "active" : ""}" data-page="${page}" ${state.page === page ? 'aria-current="page"' : ""}>${icon(i)}<span class="nav-label">${name}</span></button>`,
    )
    .join("");
  $("sidebar").innerHTML = html;
  $("bottomNav").innerHTML = html;
  $("sidebar").hidden = !!state.editor;
  $("bottomNav").hidden = !!state.editor;
  for (const nav of [$("sidebar"), $("bottomNav")])
    nav
      .querySelectorAll("[data-page]")
      .forEach((b) => (b.onclick = run(() => go(b.dataset.page))));
}
function header() {
  document.title = title() + " · Sub-Store · Script Hub";
  $("pageTitle").textContent = title();
  navigation();
  if (state.editor) {
    $("navLeft").innerHTML = button("返回", "back", "back");
    $("navRight").innerHTML =
      '<button class="text-button" data-action="save">保存</button>';
  } else {
    const list = ["subscriptions", "files"].includes(state.page);
    $("navLeft").innerHTML =
      button("刷新列表", "refresh", "reload") +
      (list
        ? button(
            "添加" + (state.page === "files" ? "文件" : "订阅"),
            "plus",
            "add",
          ) + button("搜索", "search", "search", state.search ? "active" : "")
        : "");
    $("navRight").innerHTML =
      (list
        ? button(
            preferences.simple ? "显示详细信息" : "简洁模式",
            "simple",
            "simple",
            preferences.simple ? "active" : "",
          ) +
          button(
            preferences.dual ? "切换单列" : "切换双列",
            "grid",
            "dual",
            preferences.dual ? "active" : "",
          )
        : "") +
      button(
        preferences.theme === "dark" ? "切换日间主题" : "切换夜间主题",
        preferences.theme === "dark" ? "sun" : "moon",
        "theme",
      );
  }
  bindActions(document.querySelector("header"));
}
function bindActions(root) {
  root
    .querySelectorAll("[data-action]")
    .forEach((b) => (b.onclick = run((e) => action(b.dataset.action, e))));
}
async function action(name) {
  switch (name) {
    case "back":
      await go(state.editor.kind);
      break;
    case "save":
      await saveEditor();
      break;
    case "reload":
      await loadPage();
      notify("列表已更新");
      break;
    case "add":
      openAdd();
      break;
    case "search":
      state.search = !state.search;
      renderList();
      header();
      if (state.search) $("searchInput").focus();
      break;
    case "simple":
      preferences.simple = !preferences.simple;
      persistAppearance();
      renderList();
      header();
      break;
    case "dual":
      preferences.dual = !preferences.dual;
      persistAppearance();
      renderList();
      header();
      break;
    case "theme":
      preferences.theme = preferences.theme === "light" ? "dark" : "light";
      persistAppearance();
      header();
      if (state.page === "profile") renderProfile();
      break;
  }
}
function ask(message, destructive = false) {
  if (confirmResolve) return Promise.resolve(false);
  $("confirmMessage").textContent = message;
  $("confirmOK").classList.toggle("danger-button", destructive);
  $("confirmDialog").showModal();
  return new Promise((resolve) => (confirmResolve = resolve));
}
function finishConfirm(ok) {
  $("confirmDialog").close();
  const resolve = confirmResolve;
  confirmResolve = null;
  resolve?.(ok);
}
$("confirmOK").onclick = () => finishConfirm(true);
$("confirmCancel").onclick = () => finishConfirm(false);
$("confirmDialog").addEventListener("cancel", (e) => {
  e.preventDefault();
  finishConfirm(false);
});
async function mayLeave() {
  return !state.dirty || (await ask("当前修改尚未保存，确定放弃修改？"));
}
async function go(page) {
  if (!(await mayLeave())) return;
  state.dirty = false;
  state.editor = null;
  state.page = page;
  state.query = "";
  state.sourceFilter = "all";
  state.search = false;
  state.folded = false;
  setRoute(page);
  await loadPage();
  window.scrollTo(0, 0);
}
function busyPage() {
  $("page").innerHTML =
    '<div class="empty-state"><p role="status">正在加载…</p></div>';
}
async function loadPage() {
  const generation = ++state.generation;
  header();
  if (state.page === "profile") {
    renderProfile();
    return;
  }
  busyPage();
  try {
    let rows;
    if (state.page === "shares") {
      const all = await Promise.all(
        ["subscriptions", "files"].map((kind) => api(kind)),
      );
      rows = all.flat().filter((r) => r.shared);
    } else rows = await api(state.page);
    if (generation !== state.generation) return;
    state.rows = rows;
    renderList();
  } catch (e) {
    if (generation !== state.generation) return;
    $("page").innerHTML =
      `<div class="empty-state"><h2>加载数据失败</h2><p>请检查连接后重试。</p><button class="primary-button" id="retry">重新加载</button></div>`;
    $("retry").onclick = run(loadPage);
    throw e;
  }
}
function renderList() {
  if (state.editor || state.page === "profile") return;
  const isShare = state.page === "shares",
    kind = state.page;
  const filtered = state.rows.filter(
    (r) =>
      r.name.toLocaleLowerCase().includes(state.query.toLocaleLowerCase()) &&
      (state.sourceFilter === "all" || r.source === state.sourceFilter),
  );
  const name = kind === "files" ? "文件" : isShare ? "分享" : "单条订阅";
  const filters = state.rows.length
    ? `<div class="source-filters" aria-label="按来源筛选">${[
        ["all", "全部"],
        ["remote", "远程"],
        ["local", "本地"],
      ]
        .filter(
          ([id]) => id === "all" || state.rows.some((r) => r.source === id),
        )
        .map(
          ([id, label]) =>
            `<button class="${state.sourceFilter === id ? "current" : ""}" data-source-filter="${id}" aria-pressed="${state.sourceFilter === id}">${label}</button>`,
        )
        .join("")}</div>`
    : "";
  $("page").classList.toggle("simple", preferences.simple);
  $("page").innerHTML =
    (state.search
      ? `<div class="search-box">${icon("search")}<input id="searchInput" type="search" placeholder="搜索名称" aria-label="搜索名称" value="${esc(state.query)}">${button("关闭搜索", "close", "search")}</div>`
      : "") +
    filters +
    (state.rows.length
      ? `<button class="section-heading" id="fold" aria-expanded="${!state.folded}">${name} (${filtered.length}) ${icon(state.folded ? "right" : "down")}</button><div class="list ${preferences.dual ? "dual" : ""}" id="list" ${state.folded ? "hidden" : ""}></div>`
      : `<div class="empty-state"><svg class="empty-art" aria-hidden="true"><use href="#a-empty"/></svg><h2>${isShare ? "暂无分享" : kind === "files" ? "暂无文件" : "暂无订阅"}</h2><p>${isShare ? "在订阅或文件中生成分享链接后，即可在此管理。" : kind === "files" ? "添加一个文件，开始管理你的文本和远程文件。" : "添加一个订阅，开始管理你的节点。"}</p>${isShare ? "" : '<button class="primary-button" data-action="add">立即添加</button>'}</div>`);
  bindActions($("page"));
  if (state.search)
    $("searchInput").oninput = (e) => {
      const input = e.target,
        pos = input.selectionStart;
      state.query = input.value;
      renderList();
      $("searchInput").focus();
      try {
        $("searchInput").setSelectionRange(pos, pos);
      } catch {}
    };
  document.querySelectorAll("[data-source-filter]").forEach(
    (b) =>
      (b.onclick = () => {
        state.sourceFilter = b.dataset.sourceFilter;
        renderList();
      }),
  );
  if (!state.rows.length) return;
  $("fold").onclick = () => {
    state.folded = !state.folded;
    renderList();
  };
  if (!filtered.length) {
    $("list").innerHTML = '<div class="hint">没有匹配的资源</div>';
    return;
  }
  for (const r of filtered) {
    const card = document.createElement("article");
    card.className = "resource-card";
    card.tabIndex = 0;
    card.setAttribute("aria-label", r.name);
    const details = r.source === "remote" ? "远程订阅" : "本地订阅";
    card.innerHTML =
      art(r.kind) +
      `<div class="card-content"><div class="card-title-row"><h2 class="card-title" title="${esc(r.name)}">${esc(r.name)}</h2>${r.shared ? '<span class="badge">已分享</span>' : ""}<div class="card-actions">${button("预览 " + r.name, "eye", "preview", "secondary-action")}${button("分享 " + r.name, "share", "share", "secondary-action")}${button("强制更新 " + r.name, "refresh", "refresh", "secondary-action")}${button("编辑 " + r.name, "edit", "edit")}${button("更多 " + r.name, "more", "more")}</div></div><p class="card-detail">${r.kind === "files" ? (r.source === "remote" ? "远程文件" : "本地文件") : details}${isShare ? " · " + (r.kind === "files" ? "文件" : "订阅") : ""}</p><p class="card-detail">更新于 ${esc(formatDate(r.updatedAt))}</p></div>`;
    card.onclick = run(() => edit(r.kind, r.id));
    card.onkeydown = run((e) => {
      if (e.target === card && (e.key === "Enter" || e.key === " ")) {
        e.preventDefault();
        return edit(r.kind, r.id);
      }
    });
    card.querySelectorAll("[data-action]").forEach(
      (b) =>
        (b.onclick = run(async (e) => {
          e.stopPropagation();
          switch (b.dataset.action) {
            case "edit":
              await edit(r.kind, r.id);
              break;
            case "preview":
              openExport(r);
              break;
            case "refresh":
              openExport(r, true);
              break;
            case "share":
              openShare(r);
              break;
            case "more":
              openMore(r);
              break;
          }
        })),
    );
    $("list").appendChild(card);
  }
}
function formatDate(v) {
  const d = new Date(v);
  return Number.isNaN(d.valueOf())
    ? "—"
    : d.toLocaleString("zh-CN", { hour12: false });
}
function modal(title, html) {
  ++dialogGeneration;
  state.output?.controller?.abort();
  state.output = null;
  $("dialogTitle").textContent = title;
  $("dialogBody").innerHTML = html;
  if (!$("actionDialog").open) $("actionDialog").showModal();
}
function closeModal() {
  ++dialogGeneration;
  state.output?.controller?.abort();
  $("actionDialog").close();
  state.output = null;
}
$("closeDialog").onclick = closeModal;
$("actionDialog").addEventListener("close", () => {
  state.output?.controller?.abort();
  state.output = null;
});
function openAdd() {
  const kind = state.page === "files" ? "files" : "subscriptions";
  modal(
    "添加" + (kind === "files" ? "文件" : "订阅"),
    `<button class="add-choice" id="addLocal">${art(kind)}<div><h3>${kind === "files" ? "本地文件" : "本地订阅"}</h3><p>${kind === "files" ? "创建文本文件" : "粘贴节点链接、Base64 或 Clash YAML"}</p></div>${icon("right")}</button><button class="add-choice" id="addRemote">${icon("cloud")}<div><h3>${kind === "files" ? "远程文件" : "远程订阅"}</h3><p>${kind === "files" ? "从远程地址读取文件" : "添加一个或多个订阅地址"}</p></div>${icon("right")}</button>`,
  );
  for (const source of ["Local", "Remote"])
    $("add" + source).onclick = run(async () => {
      closeModal();
      await edit(kind, null, source.toLowerCase());
    });
}
async function edit(kind, id, source = "remote") {
  if (!(await mayLeave())) return;
  const record = id
    ? await api(kind + "/" + id)
    : {
        id: null,
        kind,
        spec: { name: "", source, content: "", urls: [], processing: {} },
      };
  ++state.generation;
  state.editor = record;
  state.page = kind;
  state.dirty = false;
  setRoute("edit/" + kind + "/" + (id || "new"));
  renderEditor();
  header();
  window.scrollTo(0, 0);
}
function renderEditor() {
  const e = state.editor,
    s = e.spec,
    p = s.processing || {},
    file = e.kind === "files";
  $("page").classList.remove("simple");
  $("page").innerHTML =
    `<div class="editor-icon">${art(e.kind)}</div><form id="editorForm"><div class="form-card"><div class="field-row"><label class="required" for="name">名称</label><input id="name" type="text" maxlength="256" placeholder="请输入名称" autocomplete="off" required value="${esc(s.name)}"></div><div class="field-row"><span class="field-label required">来源</span><div class="source-options"><label><input type="radio" name="source" value="remote" ${s.source === "remote" ? "checked" : ""}><span>远程</span></label><label><input type="radio" name="source" value="local" ${s.source === "local" ? "checked" : ""}><span>本地</span></label></div></div><div class="block-field" id="localFields"><label for="content">${file ? "文件内容" : "订阅内容"}</label><textarea id="content" spellcheck="false" placeholder="${file ? "请输入文件内容" : "支持 Clash YAML、节点 URI 和 Base64 订阅"}">${esc(s.content || "")}</textarea></div><div id="remoteFields"><div class="block-field"><label for="urls">${file ? "文件链接" : "订阅链接"}</label><textarea id="urls" spellcheck="false" placeholder="${file ? "请输入远程文件地址" : "每行一个订阅地址，最多 16 个"}">${esc((s.urls || []).join("\n"))}</textarea></div><div class="block-field"><label for="headers">请求头</label><textarea id="headers" style="min-height:80px" spellcheck="false" placeholder="User-Agent: Clash">${esc(
      Object.entries(s.headers || {})
        .map(([k, v]) => k + ": " + v)
        .join("\n"),
    )}</textarea></div></div></div>${file ? "" : `<h2 class="group-title">订阅操作</h2><div class="form-card"><div class="field-row"><label for="include">保留节点</label><input id="include" type="text" placeholder="名称正则表达式" value="${esc(p.include || "")}"></div><div class="field-row"><label for="exclude">排除节点</label><input id="exclude" type="text" placeholder="名称正则表达式" value="${esc(p.exclude || "")}"></div><div class="field-row"><label for="deduplicate">节点去重</label><input class="switch" id="deduplicate" type="checkbox" ${p.deduplicate ? "checked" : ""}></div><div class="field-row"><label for="sort">按名称排序</label><input class="switch" id="sort" type="checkbox" ${p.sort ? "checked" : ""}></div></div><h2 class="group-title">重命名</h2><div class="form-card"><div id="renameList"></div><div class="editor-toolbar"><span class="hint" style="padding:0">按顺序应用正则替换</span><button type="button" id="addRename">+ 添加操作</button></div></div>`}<p class="hint">保存后，导出和分享将使用新的配置。</p><div class="editor-actions">${e.id ? '<button type="button" class="plain-button danger-button" id="delete">删除' + (file ? "文件" : "订阅") + "</button>" : '<button type="button" class="plain-button" id="cancelEdit">取消</button>'}<button type="submit" class="primary-button" id="save">保存</button></div></form>${e.id ? '<div class="editor-actions"><button class="plain-button" id="editorPreview">预览导出</button><button class="plain-button" id="editorShare">分享</button></div>' : ""}`;
  $("editorForm").oninput = () => {
    state.dirty = true;
  };
  $("editorForm").onchange = () => {
    state.dirty = true;
  };
  $("editorForm").onsubmit = run(async (ev) => {
    ev.preventDefault();
    await saveEditor();
  });
  document.querySelectorAll("input[name=source]").forEach(
    (el) =>
      (el.onchange = () => {
        state.dirty = true;
        toggleSource();
      }),
  );
  toggleSource();
  if (!file) {
    for (const rename of p.rename || []) addRename(rename);
    $("addRename").onclick = () => {
      addRename();
      state.dirty = true;
    };
  }
  if (e.id) {
    $("delete").onclick = run(() =>
      deleteResource({ id: e.id, kind: e.kind, name: e.spec.name }),
    );
    $("editorPreview").onclick = run(() => openSavedExport(false));
    $("editorShare").onclick = run(() => openSavedExport(true));
  } else $("cancelEdit").onclick = run(() => go(e.kind));
}
function toggleSource() {
  const remote =
    document.querySelector("input[name=source]:checked").value === "remote";
  $("localFields").hidden = remote;
  $("remoteFields").hidden = !remote;
}
function addRename(value = { pattern: "", replacement: "" }) {
  const row = document.createElement("div");
  row.className = "rename-item";
  row.innerHTML = `<div class="rename-fields"><input type="text" class="rename-pattern" placeholder="匹配表达式" aria-label="匹配表达式" value="${esc(value.pattern)}">${icon("right")}<input type="text" class="rename-replacement" placeholder="替换为" aria-label="替换为" value="${esc(value.replacement)}">${button("删除重命名操作", "trash", "remove")}</div>`;
  row.querySelector("button").onclick = () => {
    row.remove();
    state.dirty = true;
  };
  $("renameList").appendChild(row);
}
function editorSpec() {
  const source = document.querySelector("input[name=source]:checked").value,
    spec = { name: $("name").value.trim(), source };
  if (!spec.name) {
    $("name").focus();
    throw new Error("请输入名称");
  }
  if (source === "local") spec.content = $("content").value;
  else {
    spec.urls = $("urls")
      .value.split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    const headers = Object.create(null);
    for (const line of $("headers")
      .value.split("\n")
      .map((s) => s.trim())
      .filter(Boolean)) {
      const split = line.indexOf(":");
      if (split <= 0) throw new Error("请求头格式应为 Header-Name: value");
      headers[line.slice(0, split).trim()] = line.slice(split + 1).trim();
    }
    if (Object.keys(headers).length) spec.headers = headers;
  }
  if (state.editor.kind === "subscriptions") {
    spec.processing = {
      include: $("include").value,
      exclude: $("exclude").value,
      deduplicate: $("deduplicate").checked,
      sort: $("sort").checked,
      rename: [...document.querySelectorAll(".rename-item")].map((r) => ({
        pattern: r.querySelector(".rename-pattern").value,
        replacement: r.querySelector(".rename-replacement").value,
      })),
    };
  }
  return spec;
}
async function saveEditor() {
  if (!state.editor) return;
  const spec = editorSpec(),
    e = state.editor;
  const buttons = [...document.querySelectorAll("[data-action=save],#save")];
  buttons.forEach((b) => (b.disabled = true));
  try {
    const record = await api(
      e.kind + (e.id ? "/" + e.id : ""),
      e.id ? "PUT" : "POST",
      spec,
    );
    state.editor = record;
    state.dirty = false;
    notify("保存成功");
    await go(e.kind);
  } finally {
    buttons.forEach((b) => (b.disabled = false));
  }
}
async function openSavedExport(share) {
  if (state.dirty) {
    notify("请先保存修改，再" + (share ? "分享" : "预览"), true);
    return;
  }
  const e = state.editor,
    r = { id: e.id, kind: e.kind, name: e.spec.name };
  if (share) openShare(r);
  else openExport(r);
}
function targetPicker(selected) {
  return (
    '<div class="target-grid" role="group" aria-label="目标客户端">' +
    targets
      .map(
        ([id, name]) =>
          `<button class="target-option ${id === selected ? "selected" : ""}" data-target="${id}" aria-pressed="${id === selected}">${icon(id === "uri" ? "code" : id, id === "uri" ? "icon" : "artifact-icon")}<span>${name}</span></button>`,
      )
      .join("") +
    "</div>"
  );
}
function bindTargets(onchange) {
  document.querySelectorAll("[data-target]").forEach(
    (b) =>
      (b.onclick = () => {
        document.querySelectorAll("[data-target]").forEach((v) => {
          v.classList.toggle("selected", v === b);
          v.setAttribute("aria-pressed", v === b ? "true" : "false");
        });
        onchange(b.dataset.target);
      }),
  );
}
function openExport(record, force = false) {
  const context = {
    record,
    target: record.kind === "files" ? "text" : "clash",
    generation: 0,
    controller: null,
    body: null,
  };
  modal(
    "预览导出",
    `<p class="export-name">${esc(record.name)}</p>${record.kind === "files" ? "" : targetPicker(context.target)}<textarea class="output" id="output" readonly spellcheck="false" aria-label="导出结果" placeholder="选择格式后点击预览"></textarea><p class="output-status" id="outputStatus">${record.kind === "files" ? "" : "仅导出节点，不含策略组和分流配置。"}</p><div class="modal-actions"><button class="plain-button" id="refresh">强制更新</button><button class="primary-button" id="preview">预览</button><button class="plain-button" id="download" disabled>下载</button></div>`,
  );
  state.output = context;
  bindTargets((target) => {
    context.target = target;
    context.generation++;
    context.controller?.abort();
    context.body = null;
    $("output").value = "";
    $("download").disabled = true;
    $("outputStatus").textContent = "格式已切换，请重新预览";
    $("preview").disabled = false;
    $("refresh").disabled = false;
  });
  $("preview").onclick = run(() => preview(context, false));
  $("refresh").onclick = run(() => preview(context, true));
  $("download").onclick = () => download(context);
  if (force) run(() => preview(context, true))();
}
async function preview(context, force) {
  const generation = ++context.generation;
  context.controller?.abort();
  context.controller = new AbortController();
  $("preview").disabled = true;
  $("refresh").disabled = true;
  $("download").disabled = true;
  $("outputStatus").textContent = force ? "正在强制更新…" : "正在生成预览…";
  context.body = null;
  $("output").value = "";
  try {
    const q = new URLSearchParams({ target: context.target });
    if (force) q.set("refresh", "true");
    const r = await fetch(
      base +
        "/api/" +
        context.record.kind +
        "/" +
        context.record.id +
        "/export?" +
        q,
      {
        cache: "no-store",
        referrerPolicy: "no-referrer",
        signal: context.controller.signal,
      },
    );
    if (!r.ok) throw await responseError(r);
    const text = await r.text();
    if (state.output !== context || context.generation !== generation) return;
    context.body = text;
    $("output").value = text;
    $("download").disabled = false;
    $("outputStatus").textContent =
      "导出完成 · " + (r.headers.get("X-ScriptHub-Cache") || "OK");
  } catch (e) {
    if (
      state.output === context &&
      context.generation === generation &&
      e.name !== "AbortError"
    )
      $("outputStatus").textContent = "导出失败：" + e.message;
    throw e;
  } finally {
    if (state.output === context && context.generation === generation) {
      $("preview").disabled = false;
      $("refresh").disabled = false;
    }
  }
}
function download(context) {
  if (context.body === null) return;
  const url = URL.createObjectURL(
      new Blob([context.body], { type: "text/plain" }),
    ),
    a = document.createElement("a");
  a.href = url;
  a.download =
    context.record.name.replace(/[\\/\x00-\x1f]/g, "_") +
    "." +
    (["clash", "egern"].includes(context.target) ? "yaml" : "txt");
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
function openShare(record) {
  const context = {
    target: record.kind === "files" ? "text" : "clash",
    path: null,
  };
  modal(
    "分享",
    `<p class="export-name">${esc(record.name)}</p>${record.kind === "files" ? "" : targetPicker(context.target)}<p class="hint" style="padding:0">分享链接只允许读取当前资源。重新生成后，旧链接立即失效。</p><div class="share-result hidden" id="shareResult"><label for="shareLink">分享链接</label><input id="shareLink" readonly aria-label="分享链接"><p class="hint">链接只在本次显示，请及时复制保存。</p></div><div class="modal-actions"><button class="plain-button danger-button" id="revoke">撤销分享</button><button class="primary-button" id="share">生成分享链接</button><button class="plain-button hidden" id="copyShare">复制链接</button></div>`,
  );
  const ticket = dialogGeneration;
  const active = () => ticket === dialogGeneration && $("actionDialog").open;
  bindTargets((target) => {
    context.target = target;
    if (context.path) updateShare();
  });
  function updateShare() {
    const url = new URL(context.path, location.origin);
    if (record.kind === "subscriptions")
      url.searchParams.set("target", context.target);
    $("shareLink").value = url.href;
    $("shareResult").classList.remove("hidden");
    $("copyShare").classList.remove("hidden");
  }
  const endpoint = record.kind + "/" + record.id + "/share";
  $("share").onclick = run(async () => {
    if (!(await ask("生成新分享链接？如果已有分享链接，旧链接将立即失效。")))
      return;
    const b = $("share");
    b.disabled = true;
    try {
      const result = await api(endpoint, "POST", {});
      markShared(record, true);
      context.path = result.path;
      if (!active()) return;
      updateShare();
      $("share").textContent = "重新生成";
      notify("分享链接已生成");
      markShared(record, true);
    } finally {
      b.disabled = false;
    }
  });
  $("revoke").onclick = run(async () => {
    if (!(await ask("撤销此资源的分享？所有已分发链接将立即失效。", true)))
      return;
    await api(endpoint, "DELETE");
    markShared(record, false);
    if (!active()) return;
    context.path = null;
    $("shareLink").value = "";
    $("shareResult").classList.add("hidden");
    $("copyShare").classList.add("hidden");
    notify("分享已撤销");
    markShared(record, false);
  });
  $("copyShare").onclick = run(() => copyText($("shareLink").value));
}
function markShared(record, shared) {
  const row = state.rows.find(
    (r) => r.id === record.id && r.kind === record.kind,
  );
  if (row) row.shared = shared;
  if (state.page === "shares") state.rows = state.rows.filter((r) => r.shared);
  if (!state.editor) renderList();
}
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      notify("已复制");
      return;
    } catch {}
  }
  const t = document.createElement("textarea");
  t.value = text;
  t.style.position = "fixed";
  t.style.opacity = "0";
  const parent = $("actionDialog").open ? $("dialogBody") : document.body;
  parent.appendChild(t);
  t.select();
  const ok = document.execCommand("copy");
  t.remove();
  if (!ok) throw new Error("复制失败，请选中链接手动复制");
  notify("已复制");
}
function openMore(record) {
  modal(
    record.name,
    `<button class="settings-row" id="morePreview"><span class="settings-label">${icon("eye")}预览导出</span>${icon("right")}</button><button class="settings-row" id="moreRefresh"><span class="settings-label">${icon("refresh")}强制更新</span>${icon("right")}</button><button class="settings-row" id="moreShare"><span class="settings-label">${icon("share")}分享</span>${icon("right")}</button><button class="settings-row" id="moreEdit"><span class="settings-label">${icon("edit")}编辑</span>${icon("right")}</button><button class="settings-row danger-button" id="moreDelete"><span class="settings-label">${icon("trash")}删除</span>${icon("right")}</button>`,
  );
  $("morePreview").onclick = () => openExport(record);
  $("moreRefresh").onclick = () => openExport(record, true);
  $("moreShare").onclick = () => openShare(record);
  $("moreEdit").onclick = run(async () => {
    closeModal();
    await edit(record.kind, record.id);
  });
  $("moreDelete").onclick = run(() => deleteResource(record));
}
async function deleteResource(record) {
  if (
    !(await ask(
      "删除“" + record.name + "”？其分享链接也将失效，无法恢复。",
      true,
    ))
  )
    return;
  await api(record.kind + "/" + record.id, "DELETE");
  closeModal();
  state.dirty = false;
  state.editor = null;
  await go(record.kind);
  notify("已删除");
}
function renderProfile() {
  $("page").classList.remove("simple");
  $("page").innerHTML =
    `<div class="profile-hero">${art("subscriptions")}<div><h2>Sub-Store</h2><p>Script Hub · 原生 Go 后端</p></div></div><h2 class="group-title">外观设置</h2><div class="form-card"><button class="settings-row" id="profileTheme"><span class="settings-label">${icon("sun")}主题</span><span class="settings-value">${preferences.theme === "dark" ? "基础夜间" : "基础日间"}</span></button><div class="settings-row"><label class="settings-label" for="simpleSetting">${icon("simple")}简洁模式</label><input type="checkbox" class="switch" id="simpleSetting" ${preferences.simple ? "checked" : ""}></div><div class="settings-row"><label class="settings-label" for="dualSetting">${icon("grid")}双列显示</label><input type="checkbox" class="switch" id="dualSetting" ${preferences.dual ? "checked" : ""}></div></div><h2 class="group-title">后端</h2><div class="form-card"><div class="settings-row"><span class="settings-label">${icon("shield")}连接状态</span><span class="settings-value">当前 Backend URL</span></div><button class="settings-row" id="capabilities"><span class="settings-label">${icon("info")}兼容范围</span>${icon("right")}</button></div><h2 class="group-title">关于</h2><div class="form-card"><a class="settings-row" href="https://github.com/sub-store-org/Sub-Store-Front-End" target="_blank" rel="noreferrer noopener"><span class="settings-label">${icon("code")}原版前端</span><span class="settings-value">Sub-Store Front-End · GPL-3.0</span></a><a class="settings-row" href="https://github.com/woyin/Script-Hub" target="_blank" rel="noreferrer noopener"><span class="settings-label">${icon("code")}项目源码</span>${icon("right")}</a></div><p class="page-footer">Sub-Store 界面适配 · Script Hub<br>界面设计与素材遵循 GPL-3.0</p>`;
  $("profileTheme").onclick = () => {
    preferences.theme = preferences.theme === "light" ? "dark" : "light";
    persistAppearance();
    renderProfile();
    header();
  };
  for (const [id, key] of [
    ["simpleSetting", "simple"],
    ["dualSetting", "dual"],
  ])
    $(id).onchange = () => {
      preferences[key] = $(id).checked;
      persistAppearance();
    };
  $("capabilities").onclick = run(async () => {
    const value = await api("capabilities");
    modal("兼容范围", '<pre class="capabilities" id="capabilityBody"></pre>');
    $("capabilityBody").textContent = JSON.stringify(value, null, 2);
  });
}
window.addEventListener("beforeunload", (event) => {
  if (state.dirty) {
    event.preventDefault();
    event.returnValue = "";
  }
});
// Hash navigation keeps the private API prefix unchanged and needs no SPA fallback route.
let currentHash = location.hash;
function setRoute(route) {
  const hash = "#" + route;
  if (location.hash !== hash) history.pushState(null, "", hash);
  currentHash = hash;
}
window.addEventListener(
  "hashchange",
  run(async () => {
    if (location.hash === currentHash) return;
    const requested = location.hash;
    const parts = requested.slice(1).split("/");
    if (
      state.editor &&
      requested ===
        "#edit/" + state.editor.kind + "/" + (state.editor.id || "new")
    ) {
      currentHash = requested;
      return;
    }
    if (!(await mayLeave())) {
      history.replaceState(null, "", currentHash || "#" + state.page);
      return;
    }
    state.dirty = false;
    currentHash = requested;
    if (
      parts[0] === "edit" &&
      ["subscriptions", "files"].includes(parts[1]) &&
      parts[2]
    )
      await edit(parts[1], parts[2] === "new" ? null : parts[2]);
    else if (
      ["subscriptions", "files", "shares", "profile"].includes(parts[0])
    ) {
      state.editor = null;
      state.page = parts[0];
      await loadPage();
    }
  }),
);
run(async () => {
  const parts = location.hash.slice(1).split("/");
  if (
    parts[0] === "edit" &&
    ["subscriptions", "files"].includes(parts[1]) &&
    parts[2]
  )
    await edit(parts[1], parts[2] === "new" ? null : parts[2]);
  else {
    if (["subscriptions", "files", "shares", "profile"].includes(parts[0]))
      state.page = parts[0];
    await loadPage();
  }
})();
