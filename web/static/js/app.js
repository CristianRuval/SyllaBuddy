(function () {
  "use strict";

  var THEME_KEY = "syllabuddy-theme";

  function qs(selector, root) {
    return (root || document).querySelector(selector);
  }

  function qsa(selector, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(selector));
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  }

  function showToast(message, kind) {
    var region = qs("#toastRegion");
    if (!region) return;
    var toast = el("div", "toast" + (kind ? " toast-" + kind : ""));
    var msg = el("span", "", message);
    var closeBtn = el("button", "btn btn-sm btn-ghost", "Dismiss");
    closeBtn.type = "button";
    closeBtn.addEventListener("click", function () {
      toast.remove();
    });
    toast.appendChild(msg);
    toast.appendChild(closeBtn);
    region.appendChild(toast);
    window.setTimeout(function () {
      if (toast.parentNode) toast.remove();
    }, 4500);
  }

  async function apiFetch(url, options) {
    var opts = Object.assign({ credentials: "same-origin" }, options || {});
    if (opts.body && !(opts.body instanceof FormData)) {
      opts.headers = Object.assign({ "Content-Type": "application/json" }, opts.headers || {});
    }
    var res = await fetch(url, opts);
    var contentType = res.headers.get("content-type") || "";
    var payload = null;
    if (contentType.indexOf("application/json") !== -1) {
      payload = await res.json().catch(function () {
        return null;
      });
    } else {
      payload = await res.text().catch(function () {
        return "";
      });
    }
    if (!res.ok) {
      var errMsg =
        (payload && (payload.error || payload.detail || payload.message)) ||
        (typeof payload === "string" && payload) ||
        "Request failed (" + res.status + ")";
      var err = new Error(errMsg);
      err.status = res.status;
      throw err;
    }
    return payload;
  }

  /* ------------------------------------------------------------------------
     Theme & Global Navigation
     ------------------------------------------------------------------------ */
  function getEffectiveTheme() {
    var current = document.documentElement.getAttribute("data-theme");
    return current === "dark" ? "dark" : "light";
  }

  function applyTheme(choice) {
    var resolved = choice;
    try {
      if (choice === "system") {
        localStorage.removeItem(THEME_KEY);
        resolved =
          window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches
            ? "dark"
            : "light";
      } else {
        localStorage.setItem(THEME_KEY, choice);
      }
    } catch (e) {
      // Ignore storage failures
    }
    document.documentElement.setAttribute("data-theme", resolved);
    syncThemeUI();
  }

  function syncThemeUI() {
    var effective = getEffectiveTheme();
    var toggle = qs("#themeToggleBtn");
    if (toggle) {
      toggle.setAttribute(
        "title",
        effective === "dark" ? "Switch to Light Mode" : "Switch to Dark Mode"
      );
      toggle.setAttribute(
        "aria-label",
        effective === "dark" ? "Switch to Light Mode" : "Switch to Dark Mode"
      );
    }
    var saved = null;
    try {
      saved = localStorage.getItem(THEME_KEY);
    } catch (e) {
      saved = null;
    }
    var activeChoice = saved === "light" || saved === "dark" ? saved : "system";
    qsa("[data-theme-choice]").forEach(function (btn) {
      var isActive = btn.getAttribute("data-theme-choice") === activeChoice;
      btn.classList.toggle("active", isActive);
    });
  }

  function initGlobalChrome() {
    syncThemeUI();
    var toggle = qs("#themeToggleBtn");
    if (toggle) {
      toggle.addEventListener("click", function () {
        applyTheme(getEffectiveTheme() === "dark" ? "light" : "dark");
      });
    }

    var menuBtn = qs("#navMenuBtn");
    var nav = qs("#primaryNav");
    if (menuBtn && nav) {
      menuBtn.addEventListener("click", function () {
        var open = nav.classList.toggle("open");
        menuBtn.setAttribute("aria-expanded", open ? "true" : "false");
      });
    }

    var logoutBtn = qs("#logoutBtn");
    if (logoutBtn) {
      logoutBtn.addEventListener("click", async function () {
        logoutBtn.disabled = true;
        try {
          await apiFetch("/api/auth/logout", { method: "POST" });
        } catch (e) {
          // Proceed home anyway
        }
        window.location.href = "/";
      });
    }
  }

  /* ------------------------------------------------------------------------
     Login / Register Page
     ------------------------------------------------------------------------ */
  function initLoginPage() {
    var tabSignIn = qs("#tabSignIn");
    var tabRegister = qs("#tabRegister");
    var loginForm = qs("#loginForm");
    var registerForm = qs("#registerForm");
    var errBox = qs("#authError");

    function showError(msg) {
      if (!errBox) return;
      errBox.textContent = msg;
      errBox.classList.remove("hidden");
    }

    function clearError() {
      if (!errBox) return;
      errBox.textContent = "";
      errBox.classList.add("hidden");
    }

    function setMode(mode) {
      clearError();
      var isReg = mode === "register";
      if (tabSignIn && tabRegister) {
        tabSignIn.classList.toggle("active", !isReg);
        tabRegister.classList.toggle("active", isReg);
        tabSignIn.setAttribute("aria-selected", !isReg ? "true" : "false");
        tabRegister.setAttribute("aria-selected", isReg ? "true" : "false");
      }
      if (loginForm && registerForm) {
        loginForm.classList.toggle("hidden", isReg);
        registerForm.classList.toggle("hidden", !isReg);
      }
    }

    if (tabSignIn) tabSignIn.addEventListener("click", function () { setMode("login"); });
    if (tabRegister) tabRegister.addEventListener("click", function () { setMode("register"); });

    var params = new URLSearchParams(window.location.search);
    if (params.get("mode") === "register") {
      setMode("register");
    }

    if (loginForm) {
      loginForm.addEventListener("submit", async function (e) {
        e.preventDefault();
        clearError();
        var btn = qs("#loginSubmitBtn");
        if (btn) btn.disabled = true;
        try {
          await apiFetch("/api/auth/login", {
            method: "POST",
            body: JSON.stringify({
              username: qs("#loginUsername").value.trim(),
              password: qs("#loginPassword").value
            })
          });
          window.location.href = "/dashboard";
        } catch (err) {
          showError(err.message);
          if (btn) btn.disabled = false;
        }
      });
    }

    if (registerForm) {
      registerForm.addEventListener("submit", async function (e) {
        e.preventDefault();
        clearError();
        var btn = qs("#registerSubmitBtn");
        if (btn) btn.disabled = true;
        var inviteEl = qs("#regInviteCode");
        try {
          await apiFetch("/api/auth/register", {
            method: "POST",
            body: JSON.stringify({
              username: qs("#regUsername").value.trim(),
              email: qs("#regEmail").value.trim(),
              password: qs("#regPassword").value,
              invite_code: inviteEl ? inviteEl.value.trim() : ""
            })
          });
          window.location.href = "/dashboard";
        } catch (err) {
          showError(err.message);
          if (btn) btn.disabled = false;
        }
      });
    }

    var clientId = document.body.getAttribute("data-google-client-id");
    var slot = qs("#googleSignInSlot");
    if (clientId && slot) {
      var attempts = 0;
      var waitGIS = window.setInterval(function () {
        attempts++;
        if (window.google && window.google.accounts && window.google.accounts.id) {
          window.clearInterval(waitGIS);
          window.google.accounts.id.initialize({
            client_id: clientId,
            callback: async function (response) {
              clearError();
              try {
                await apiFetch("/api/auth/google", {
                  method: "POST",
                  body: JSON.stringify({ credential: response.credential })
                });
                window.location.href = "/dashboard";
              } catch (err) {
                showError(err.message);
              }
            }
          });
          window.google.accounts.id.renderButton(slot, {
            theme: getEffectiveTheme() === "dark" ? "filled_black" : "outline",
            size: "large",
            shape: "pill"
          });
        } else if (attempts > 40) {
          window.clearInterval(waitGIS);
        }
      }, 150);
    }
  }

  /* ------------------------------------------------------------------------
     Upload Helper + Polling
     ------------------------------------------------------------------------ */
  function normalizeSyllabiList(data) {
    if (Array.isArray(data)) return data;
    if (data && Array.isArray(data.syllabi)) return data.syllabi;
    return [];
  }

  function normalizeEventGroups(data) {
    if (Array.isArray(data)) return data;
    if (data && Array.isArray(data.courses)) return data.courses;
    if (data && Array.isArray(data.events)) return data.events;
    return [];
  }

  async function uploadSyllabusPDF(file, onStatus, onDone) {
    if (!file) return;
    if (file.type && file.type !== "application/pdf" && !file.name.toLowerCase().endsWith(".pdf")) {
      showToast("Please select a valid PDF file.", "error");
      return;
    }
    var formData = new FormData();
    formData.append("file", file);
    if (onStatus) onStatus("Uploading PDF and starting Gemini parser...");
    try {
      var created = await apiFetch("/api/syllabi/upload", {
        method: "POST",
        body: formData
      });
      showToast("Syllabus uploaded. Parsing events...", "success");
      if (onStatus) onStatus("Parsing syllabus with Gemini AI...");
      if (onDone) onDone(created);

      var targetId = created && created.id;
      if (targetId) {
        var polls = 0;
        var timer = window.setInterval(async function () {
          polls++;
          try {
            var list = normalizeSyllabiList(await apiFetch("/api/syllabi"));
            var match = list.find(function (s) {
              return s.id === targetId;
            });
            if ((match && match.parser_output && (match.parser_output.events || match.parser_output.error)) || polls >= 24) {
              window.clearInterval(timer);
              if (onStatus) {
                if (match && match.parser_output && match.parser_output.error) {
                  onStatus("Extraction failed: " + match.parser_output.error);
                  showToast(match.parser_output.error, "error");
                } else {
                  onStatus(
                    match && match.parser_output
                      ? "Syllabus parsed and ready."
                      : "Still processing in the background — check Syllabi shortly."
                  );
                }
              }
              if (onDone) onDone(match || created);
            }
          } catch (e) {
            window.clearInterval(timer);
          }
        }, 2500);
      }
    } catch (err) {
      if (onStatus) onStatus("Upload failed: " + err.message);
      showToast(err.message, "error");
    }
  }

  /* ------------------------------------------------------------------------
     Dashboard Overview
     ------------------------------------------------------------------------ */
  function initDashboardPage() {
    var dropzone = qs("#uploadDropzone");
    var fileInput = qs("#syllabusFileInput");
    var statusBanner = qs("#uploadStatusBanner");
    var statusText = qs("#uploadStatusText");

    async function refreshOverview() {
      try {
        var results = await Promise.all([
          apiFetch("/api/syllabi").catch(function () { return []; }),
          apiFetch("/api/events/all").catch(function () { return []; })
        ]);
        var syllabi = normalizeSyllabiList(results[0]);
        var groups = normalizeEventGroups(results[1]);

        var statSyl = qs("#statSyllabiCount");
        if (statSyl) statSyl.textContent = String(syllabi.length);

        var allEvents = [];
        groups.forEach(function (g) {
          var courseLabel = g.course_name || g.courseName || "Course";
          (g.events || []).forEach(function (ev) {
            allEvents.push(Object.assign({ _course: courseLabel }, ev));
          });
        });

        var statEv = qs("#statEventsCount");
        if (statEv) statEv.textContent = String(allEvents.length);

        var today = new Date().toISOString().slice(0, 10);
        var futureEvents = allEvents
          .filter(function (ev) {
            return ev.date && ev.date >= today;
          })
          .sort(function (a, b) {
            return String(a.date).localeCompare(String(b.date));
          });

        var statUp = qs("#statUpcomingCount");
        if (statUp) statUp.textContent = String(futureEvents.length);

        var listEl = qs("#upcomingEventsList");
        if (listEl) {
          listEl.textContent = "";
          var upcoming = futureEvents.slice(0, 5);

          if (upcoming.length === 0) {
            listEl.appendChild(
              el("p", "small muted", allEvents.length ? "No upcoming dates remaining." : "Upload a syllabus to see upcoming exams and assignments here.")
            );
          } else {
            upcoming.forEach(function (ev) {
              var row = el("div", "course-item");
              var head = el("div", "course-item-head");
              head.appendChild(el("strong", "", ev.title || "Untitled Event"));
              var pillClass = ev.type === "exam" ? "pill-danger" : ev.type === "holiday" ? "pill-success" : "pill-info";
              head.appendChild(el("span", "pill " + pillClass, ev.type || "event"));
              row.appendChild(head);
              row.appendChild(el("span", "small muted", ev._course + " · " + ev.date));
              listEl.appendChild(row);
            });
          }
        }
      } catch (e) {
        // Non-fatal
      }
    }

    if (dropzone && fileInput) {
      dropzone.addEventListener("click", function () { fileInput.click(); });
      dropzone.addEventListener("keydown", function (e) {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          fileInput.click();
        }
      });
      dropzone.addEventListener("dragover", function (e) {
        e.preventDefault();
        dropzone.classList.add("dragover");
      });
      dropzone.addEventListener("dragleave", function () {
        dropzone.classList.remove("dragover");
      });
      dropzone.addEventListener("drop", function (e) {
        e.preventDefault();
        dropzone.classList.remove("dragover");
        var file = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
        if (file) {
          uploadSyllabusPDF(
            file,
            function (msg) {
              if (statusBanner && statusText) {
                statusBanner.classList.remove("hidden");
                statusText.textContent = msg;
              }
            },
            refreshOverview
          );
        }
      });
      fileInput.addEventListener("change", function () {
        var file = fileInput.files && fileInput.files[0];
        if (file) {
          uploadSyllabusPDF(
            file,
            function (msg) {
              if (statusBanner && statusText) {
                statusBanner.classList.remove("hidden");
                statusText.textContent = msg;
              }
            },
            refreshOverview
          );
          fileInput.value = "";
        }
      });
    }

    refreshOverview();
  }

  /* ------------------------------------------------------------------------
     Syllabi Library Page
     ------------------------------------------------------------------------ */
  function initSyllabiPage() {
    var grid = qs("#syllabiGrid");
    var empty = qs("#syllabiEmptyState");
    var fileInput = qs("#syllabiFileInput");

    async function loadSyllabi() {
      if (!grid) return;
      try {
        var list = normalizeSyllabiList(await apiFetch("/api/syllabi"));
        grid.textContent = "";
        if (list.length === 0) {
          if (empty) empty.classList.remove("hidden");
          return;
        }
        if (empty) empty.classList.add("hidden");

        list.forEach(function (syl) {
          var card = el("article", "card syllabus-card");

          var thumbWrap = el("div", "syllabus-thumb");
          var img = document.createElement("img");
          img.src = "/api/thumbnail/" + encodeURIComponent(syl.id);
          img.alt = (syl.course_name || "Syllabus") + " preview";
          img.loading = "lazy";
          img.addEventListener("error", function () {
            thumbWrap.textContent = "";
            thumbWrap.appendChild(el("span", "pill pill-neutral", "PDF Document"));
          });
          thumbWrap.appendChild(img);
          card.appendChild(thumbWrap);

          var body = el("div", "syllabus-body");
          var header = el("div", "flex items-center justify-between gap-2");
          header.appendChild(el("h2", "card-title", syl.course_name || "Untitled Course"));
          var parseErr = syl.parser_output && syl.parser_output.error ? String(syl.parser_output.error) : "";
          var isParsed = Boolean(!parseErr && syl.parser_output && Array.isArray(syl.parser_output.events));
          var badgeClass = parseErr ? "pill-danger" : isParsed ? "pill-success" : "pill-warn";
          var badgeLabel = parseErr ? "Parse Failed" : isParsed ? "Parsed" : "Processing";
          header.appendChild(el("span", "pill " + badgeClass, badgeLabel));
          body.appendChild(header);

          var dateStr = syl.upload_date ? String(syl.upload_date).slice(0, 10) : "";
          var eventCount = isParsed ? syl.parser_output.events.length : 0;
          var metaText = parseErr
            ? parseErr
            : (dateStr ? "Uploaded " + dateStr : "Uploaded recently") +
              (isParsed ? " · " + eventCount + " events" : " · Extracting dates...");
          body.appendChild(el("p", parseErr ? "small text-danger" : "small muted", metaText));

          var actions = el("div", "syllabus-actions");
          var pdfLink = el("a", "btn btn-sm", "Open PDF");
          pdfLink.href = "/api/view-pdf/" + encodeURIComponent(syl.id);
          pdfLink.target = "_blank";
          pdfLink.rel = "noopener noreferrer";
          actions.appendChild(pdfLink);

          if (parseErr || (isParsed && eventCount === 0)) {
            var retryBtn = el("button", "btn btn-sm btn-primary", "Retry Parse");
            retryBtn.type = "button";
            retryBtn.addEventListener("click", async function () {
              retryBtn.disabled = true;
              retryBtn.textContent = "Retrying...";
              try {
                await apiFetch("/api/syllabi/" + encodeURIComponent(syl.id) + "/reparse", { method: "POST" });
                showToast("Re-queued for Gemini AI extraction.", "success");
                window.setTimeout(loadSyllabi, 2000);
              } catch (err) {
                showToast(err.message, "error");
                retryBtn.disabled = false;
                retryBtn.textContent = "Retry Parse";
              }
            });
            actions.appendChild(retryBtn);
          }

          if (isParsed) {
            var calLink = el("a", "btn btn-sm btn-primary", "Calendar");
            calLink.href = "/dashboard/calendar?syllabus_id=" + encodeURIComponent(syl.id);
            actions.appendChild(calLink);
          }

          var delBtn = el("button", "btn btn-sm btn-danger", "Delete");
          delBtn.type = "button";
          var confirmState = false;
          delBtn.addEventListener("click", async function () {
            if (!confirmState) {
              confirmState = true;
              delBtn.textContent = "Confirm Delete";
              window.setTimeout(function () {
                if (delBtn.isConnected) {
                  confirmState = false;
                  delBtn.textContent = "Delete";
                }
              }, 3500);
              return;
            }
            delBtn.disabled = true;
            try {
              await apiFetch("/api/syllabi/" + encodeURIComponent(syl.id), { method: "DELETE" });
              showToast("Syllabus deleted.", "success");
              loadSyllabi();
            } catch (err) {
              showToast(err.message, "error");
              delBtn.disabled = false;
            }
          });
          actions.appendChild(delBtn);

          body.appendChild(actions);
          card.appendChild(body);
          grid.appendChild(card);
        });
      } catch (err) {
        showToast(err.message, "error");
      }
    }

    if (fileInput) {
      fileInput.addEventListener("change", function () {
        var file = fileInput.files && fileInput.files[0];
        if (file) {
          uploadSyllabusPDF(file, null, loadSyllabi);
          fileInput.value = "";
        }
      });
    }

    loadSyllabi();
  }

  /* ------------------------------------------------------------------------
     Calendar Page (Course-grouped tables, inline date edit, Google Sync)
     ------------------------------------------------------------------------ */
  function initCalendarPage() {
    var container = qs("#calendarCoursesContainer");
    var searchInput = qs("#calSearchInput");
    var typeFilter = qs("#calTypeFilter");
    var courseFilter = qs("#calCourseFilter");
    var syncBtn = qs("#syncGoogleCalBtn");
    var sectionEl = qs("[data-filter-syllabus-id]");
    var initialSylId =
      new URLSearchParams(window.location.search).get("syllabus_id") ||
      (sectionEl ? sectionEl.getAttribute("data-filter-syllabus-id") : "") ||
      "";
    if (initialSylId === "0") initialSylId = "";

    var allGroups = [];

    function renderGroups() {
      if (!container) return;
      var q = searchInput ? searchInput.value.trim().toLowerCase() : "";
      var typeVal = typeFilter ? typeFilter.value : "all";
      var courseVal = courseFilter ? courseFilter.value : "all";

      container.textContent = "";
      var renderedAny = false;

      allGroups.forEach(function (group) {
        var sylId = String(group.syllabus_id || group.id || "");
        var courseName = group.course_name || group.courseName || "Course";
        if (courseVal !== "all" && sylId !== courseVal) return;

        var filteredEvents = (group.events || []).filter(function (ev) {
          if (typeVal !== "all" && String(ev.type || "other").toLowerCase() !== typeVal) {
            return false;
          }
          if (q) {
            var hay = [courseName, ev.title, ev.code, ev.description, ev.date]
              .join(" ")
              .toLowerCase();
            if (hay.indexOf(q) === -1) return false;
          }
          return true;
        });

        if (filteredEvents.length === 0) return;
        renderedAny = true;

        var card = el("section", "card");
        var head = el("div", "card-header");
        var left = el("div");
        left.appendChild(el("h2", "card-title", courseName));
        left.appendChild(
          el("span", "small muted", filteredEvents.length + " event" + (filteredEvents.length === 1 ? "" : "s"))
        );
        head.appendChild(left);

        var icsLink = el("a", "btn btn-sm", "Export Course (.ics)");
        icsLink.href = "/api/download-calendar/" + encodeURIComponent(sylId);
        icsLink.setAttribute("download", courseName.replace(/[^a-zA-Z0-9_-]+/g, "_") + ".ics");
        head.appendChild(icsLink);
        card.appendChild(head);

        var wrap = el("div", "table-wrap");
        var table = el("table", "table");
        var thead = document.createElement("thead");
        var hrow = document.createElement("tr");
        ["Event", "Type", "Date (Inline Edit)", "Details", ""].forEach(function (label) {
          hrow.appendChild(el("th", "", label));
        });
        thead.appendChild(hrow);
        table.appendChild(thead);

        var tbody = document.createElement("tbody");
        filteredEvents.forEach(function (ev) {
          var tr = document.createElement("tr");
          var tdTitle = el("td", "");
          tdTitle.appendChild(el("strong", "", ev.title || "Untitled"));
          if (ev.code) {
            tdTitle.appendChild(el("div", "small muted", ev.code));
          }
          tr.appendChild(tdTitle);

          var tdType = el("td", "");
          var evType = String(ev.type || "other").toLowerCase();
          var pillKind =
            evType === "exam"
              ? "pill-danger"
              : evType === "assignment"
                ? "pill-info"
                : evType === "holiday"
                  ? "pill-success"
                  : "pill-neutral";
          tdType.appendChild(el("span", "pill " + pillKind, evType));
          tr.appendChild(tdType);

          var tdDate = el("td", "");
          var dateInput = document.createElement("input");
          dateInput.type = "date";
          dateInput.className = "input input-sm";
          dateInput.style.width = "160px";
          dateInput.value = ev.date || "";
          dateInput.setAttribute("aria-label", "Date for " + (ev.title || "event"));
          tdDate.appendChild(dateInput);
          tr.appendChild(tdDate);

          var tdDesc = el("td", "small muted", ev.description || "—");
          tr.appendChild(tdDesc);

          var tdAct = el("td", "");
          var saveBtn = el("button", "btn btn-sm", "Save");
          saveBtn.type = "button";
          saveBtn.disabled = true;
          dateInput.addEventListener("change", function () {
            saveBtn.disabled = dateInput.value === (ev.date || "");
          });
          saveBtn.addEventListener("click", async function () {
            var newDate = dateInput.value;
            if (!newDate) return;
            saveBtn.disabled = true;
            saveBtn.textContent = "Saving...";
            try {
              await apiFetch("/api/syllabi/" + encodeURIComponent(sylId) + "/events", {
                method: "PUT",
                body: JSON.stringify({
                  title: ev.title,
                  old_date: ev.date || "",
                  new_date: newDate,
                  date: newDate
                })
              });
              ev.date = newDate;
              saveBtn.textContent = "Saved";
              showToast("Updated date for " + ev.title, "success");
              window.setTimeout(function () {
                saveBtn.textContent = "Save";
              }, 1600);
            } catch (err) {
              showToast(err.message, "error");
              saveBtn.textContent = "Save";
              saveBtn.disabled = false;
            }
          });
          tdAct.appendChild(saveBtn);
          tr.appendChild(tdAct);

          tbody.appendChild(tr);
        });

        table.appendChild(tbody);
        wrap.appendChild(table);
        card.appendChild(wrap);
        container.appendChild(card);
      });

      if (!renderedAny) {
        var emptyCard = el("div", "card");
        emptyCard.appendChild(el("h2", "card-title", "No matching events"));
        emptyCard.appendChild(
          el("p", "small muted mt-2", "Upload a syllabus or adjust your search and filter criteria.")
        );
        container.appendChild(emptyCard);
      }
    }

    async function loadEvents() {
      try {
        allGroups = normalizeEventGroups(await apiFetch("/api/events/all"));
        if (courseFilter) {
          courseFilter.textContent = "";
          var optAll = el("option", "", "All courses");
          optAll.value = "all";
          courseFilter.appendChild(optAll);
          allGroups.forEach(function (g) {
            var opt = el("option", "", g.course_name || g.courseName || "Course");
            opt.value = String(g.syllabus_id || g.id || "");
            courseFilter.appendChild(opt);
          });
          if (initialSylId) {
            var hasOption = Array.prototype.some.call(courseFilter.options, function (opt) {
              return opt.value === initialSylId;
            });
            if (hasOption) {
              courseFilter.value = initialSylId;
            }
          }
        }
        renderGroups();
      } catch (err) {
        if (container) {
          container.textContent = "";
          var errCard = el("div", "card");
          errCard.appendChild(el("p", "small muted", "Could not load events: " + err.message));
          container.appendChild(errCard);
        }
      }
    }

    if (searchInput) searchInput.addEventListener("input", renderGroups);
    if (typeFilter) typeFilter.addEventListener("change", renderGroups);
    if (courseFilter) courseFilter.addEventListener("change", renderGroups);

    if (syncBtn) {
      syncBtn.addEventListener("click", function () {
        var clientId = document.body.getAttribute("data-google-client-id");
        if (!clientId || !window.google || !window.google.accounts || !window.google.accounts.oauth2) {
          showToast("Google Calendar client is still loading or not configured.", "error");
          return;
        }
        var tokenClient = window.google.accounts.oauth2.initTokenClient({
          client_id: clientId,
          scope: "https://www.googleapis.com/auth/calendar.events",
          callback: async function (tokenResp) {
            if (!tokenResp || !tokenResp.access_token) {
              showToast("Google authorization was cancelled.", "error");
              return;
            }
            syncBtn.disabled = true;
            syncBtn.textContent = "Syncing...";
            try {
              var body = { google_token: tokenResp.access_token };
              if (courseFilter && courseFilter.value !== "all") {
                body.syllabus_id = Number(courseFilter.value) || undefined;
              }
              var res = await apiFetch("/api/events/sync-google", {
                method: "POST",
                body: JSON.stringify(body)
              });
              showToast(
                (res && res.message) || "Events synced to Google Calendar.",
                "success"
              );
            } catch (err) {
              showToast(err.message, "error");
            } finally {
              syncBtn.disabled = false;
              syncBtn.textContent = "Sync to Google Calendar";
            }
          }
        });
        tokenClient.requestAccessToken({ prompt: "consent" });
      });
    }

    loadEvents();
  }

  /* ------------------------------------------------------------------------
     Settings Page
     ------------------------------------------------------------------------ */
  function initSettingsPage() {
    qsa("[data-theme-choice]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        applyTheme(btn.getAttribute("data-theme-choice"));
        showToast("Theme updated.", "success");
      });
    });

    var profileForm = qs("#profileForm");
    if (profileForm) {
      profileForm.addEventListener("submit", async function (e) {
        e.preventDefault();
        var username = qs("#profileUsername").value.trim();
        try {
          await apiFetch("/api/users/me/profile", {
            method: "PUT",
            body: JSON.stringify({ username: username })
          });
          showToast("Username updated.", "success");
        } catch (err) {
          showToast(err.message, "error");
        }
      });
    }

    var passwordForm = qs("#passwordForm");
    if (passwordForm) {
      passwordForm.addEventListener("submit", async function (e) {
        e.preventDefault();
        try {
          await apiFetch("/api/users/me/password", {
            method: "PUT",
            body: JSON.stringify({
              current_password: qs("#currentPassword").value,
              new_password: qs("#newPassword").value
            })
          });
          qs("#currentPassword").value = "";
          qs("#newPassword").value = "";
          showToast("Password updated.", "success");
        } catch (err) {
          showToast(err.message, "error");
        }
      });
    }

    var showDelBtn = qs("#showDeleteAccountBtn");
    var cancelDelBtn = qs("#cancelDeleteAccountBtn");
    var confirmBox = qs("#deleteAccountConfirmBox");
    var delForm = qs("#deleteAccountForm");

    if (showDelBtn && confirmBox) {
      showDelBtn.addEventListener("click", function () {
        confirmBox.classList.remove("hidden");
      });
    }
    if (cancelDelBtn && confirmBox) {
      cancelDelBtn.addEventListener("click", function () {
        confirmBox.classList.add("hidden");
      });
    }
    if (delForm) {
      delForm.addEventListener("submit", async function (e) {
        e.preventDefault();
        var val = (qs("#deleteConfirmInput").value || "").trim();
        if (val !== "DELETE") {
          showToast("Type DELETE exactly to confirm account deletion.", "error");
          return;
        }
        try {
          await apiFetch("/api/users/me", {
            method: "DELETE",
            body: JSON.stringify({ confirm: true })
          });
          window.location.href = "/";
        } catch (err) {
          showToast(err.message, "error");
        }
      });
    }
  }

  /* ------------------------------------------------------------------------
     Boot
     ------------------------------------------------------------------------ */
  document.addEventListener("DOMContentLoaded", function () {
    initGlobalChrome();
    var page = document.body.getAttribute("data-page");
    if (page === "login") initLoginPage();
    if (page === "dashboard") initDashboardPage();
    if (page === "syllabi") initSyllabiPage();
    if (page === "calendar") initCalendarPage();
    if (page === "settings") initSettingsPage();
  });
})();
