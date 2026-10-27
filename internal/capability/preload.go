package capability

// PreloadScript defines window.bowknot before any app page script runs. It
// wraps the bound BowknotCapability service: the inner reply {ok, code,
// message, data} becomes a resolved value or a thrown Error carrying code.
const PreloadScript = `
(() => {
  const invoke = async (method, ...args) => {
    const wrapped = await window.mygo.call("BowknotCapability." + method, ...args);
    const reply = wrapped && typeof wrapped === "object" && "v" in wrapped ? wrapped.v : wrapped;
    if (!reply || reply.ok !== true) {
      const error = new Error((reply && reply.message) || "Capability call failed");
      error.code = (reply && reply.code) || "internal";
      throw error;
    }
    return reply.data === undefined ? null : reply.data;
  };

  const appData = {
    read: (path) => invoke("AppDataRead", String(path ?? "")),
    write: (path, text) => invoke("AppDataWrite", String(path ?? ""), String(text ?? "")),
    list: (path) => invoke("AppDataList", String(path ?? "")),
    remove: (path) => invoke("AppDataRemove", String(path ?? "")),
  };

  const files = {
    pickFile: () => invoke("PickFile"),
    pickDirectory: () => invoke("PickDirectory"),
    readGranted: (token, relative) => invoke("ReadGranted", String(token ?? ""), String(relative ?? "")),
    writeGranted: (token, relative, text) =>
      invoke("WriteGranted", String(token ?? ""), String(relative ?? ""), String(text ?? "")),
    listGranted: (token, relative) => invoke("ListGranted", String(token ?? ""), String(relative ?? "")),
  };

  const clipboard = {
    readText: () => invoke("ReadClipboardText").then((data) => (data && data.content) || ""),
    writeText: (text) => invoke("WriteClipboardText", String(text ?? "")),
  };

  window.bowknot = {
    appData,
    files,
    clipboard,
    notify: (options) => {
      const value = options || {};
      return invoke("Notify", String(value.title ?? ""), String(value.body ?? ""));
    },
    openExternal: (url) => invoke("OpenExternal", String(url ?? "")),
  };
})();
`
