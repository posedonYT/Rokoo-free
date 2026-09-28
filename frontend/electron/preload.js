const { contextBridge } = require("electron");

contextBridge.exposeInMainWorld("rokko", { desktop: true });
