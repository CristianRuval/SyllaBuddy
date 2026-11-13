(function () {
  try {
    var saved = localStorage.getItem("syllabuddy-theme");
    var serverTheme = document.documentElement.getAttribute("data-server-theme");
    var prefersDark =
      window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
    var theme =
      saved === "light" || saved === "dark"
        ? saved
        : serverTheme === "light" || serverTheme === "dark"
          ? serverTheme
          : prefersDark
            ? "dark"
            : "light";
    document.documentElement.setAttribute("data-theme", theme);
  } catch (err) {
    document.documentElement.setAttribute("data-theme", "light");
  }
})();
