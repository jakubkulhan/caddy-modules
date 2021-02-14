const app = document.getElementById("app");
if (app.childNodes.length === 0) {
    const tpl = document.getElementById("app-template");
    app.appendChild(document.importNode(tpl.content, true));

    tpl.parentNode.removeChild(tpl);
    document.currentScript.parentNode.removeChild(document.currentScript);
}