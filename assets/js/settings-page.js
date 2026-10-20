// Settings pages and account dialogs. This code used to sit in the pages as
// inline scripts; Raven's pages may only run scripts from its own files.
// Setup that ran when a part of the page arrived now runs through
// ravenOnContent (actions.js) when that part appears.

// Compose and display tab
(function() {
  function composeAutosaveEnabled() {
    var toggle = document.querySelector('[data-compose-autosave-setting="enabled"]');
    return !toggle || toggle.checked;
  }
  function selectedComposeAutosaveConditions() {
    var input = document.querySelector('[data-compose-autosave-conditions-value]');
    return (input && input.value ? input.value : '').split(',').filter(Boolean);
  }
  function saveComposeAutosaveConditions(values) {
    var input = document.querySelector('[data-compose-autosave-conditions-value]');
    if (input) input.value = values.join(',');
    if (typeof RavenSettings !== 'undefined') RavenSettings.set('compose_autosave_conditions', values.join(','));
  }
  function composeAutosaveConditionLabel(values) {
    if (!values.length) return 'No conditions';
    return values.length === 1 ? '1 condition' : values.length + ' conditions';
  }
  function syncComposeAutosaveControls() {
    var enabled = composeAutosaveEnabled();
    var selected = selectedComposeAutosaveConditions();
    document.querySelectorAll('[data-compose-autosave-dependent] .select-trigger, [data-compose-autosave-conditions-button], [data-compose-autosave-min-chars]').forEach(function(el) {
      el.disabled = !enabled;
      el.classList.toggle('opacity-50', !enabled);
    });
    document.querySelectorAll('[data-compose-autosave-condition-check]').forEach(function(check) {
      check.classList.toggle('opacity-0', selected.indexOf(check.dataset.composeAutosaveConditionCheck) === -1);
    });
    var label = document.querySelector('[data-compose-autosave-conditions-label]');
    if (label) label.textContent = composeAutosaveConditionLabel(selected);
    var minChars = document.querySelector('[data-compose-autosave-min-chars]');
    if (minChars) minChars.disabled = !enabled || selected.indexOf('chars') === -1;
  }
  document.addEventListener('click', function(e) {
    var item = e.target.closest && e.target.closest('[data-compose-autosave-condition-item]');
    if (!item || e.target.closest('[data-compose-autosave-min-chars]')) return;
    var selected = selectedComposeAutosaveConditions();
    var value = item.dataset.composeAutosaveConditionItem;
    var index = selected.indexOf(value);
    if (index === -1) selected.push(value);
    else selected.splice(index, 1);
    saveComposeAutosaveConditions(selected);
    syncComposeAutosaveControls();
  });
  document.addEventListener('change', function(e) {
    var defaultComposeView = e.target.closest('input[name="default_new_compose_view"], input[name="default_reply_compose_view"]');
    if (defaultComposeView && typeof RavenSettings !== 'undefined') {
      RavenSettings.set(defaultComposeView.name, defaultComposeView.value);
    }
    var mailPaneLayout = e.target.closest('input[name="mail_pane_layout"]');
    if (mailPaneLayout && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('mail_pane_layout', mailPaneLayout.value);
    }
    var autosaveToggle = e.target.closest('[data-compose-autosave-setting="enabled"]');
    if (autosaveToggle && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('compose_autosave_enabled', autosaveToggle.checked ? 'true' : 'false');
      syncComposeAutosaveControls();
    }
    var minChars = e.target.closest('[data-compose-autosave-min-chars]');
    if (minChars && typeof RavenSettings !== 'undefined') {
      var n = Math.max(1, Math.min(1000, parseInt(minChars.value.replace(/\D/g, '') || '30', 10) || 30));
      minChars.value = n;
      RavenSettings.set('compose_autosave_min_chars', String(n));
    }
    var autosaveDebounce = e.target.closest('input[name="compose_autosave_debounce"]');
    if (autosaveDebounce && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('compose_autosave_debounce', autosaveDebounce.value);
    }
    var senderDisplay = e.target.closest('input[name="sender_display"]');
    if (senderDisplay && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('sender_display', senderDisplay.value);
    }
    var mailListNavigation = e.target.closest('input[name="mail_list_navigation"]');
    if (mailListNavigation && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('mail_list_navigation', mailListNavigation.value);
    }
    var threadOrder = e.target.closest('input[name="thread_order"]');
    if (threadOrder && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('thread_order', threadOrder.value);
    }
    var autoMarkRead = e.target.closest('input[name="auto_mark_read_after"]');
    if (autoMarkRead && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('auto_mark_read_after', autoMarkRead.value);
    }
    var undoSend = e.target.closest('input[name="undo_send_seconds"]');
    if (undoSend && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('undo_send_seconds', undoSend.value);
    }
    var calendarReminder = e.target.closest('input[name="calendar_reminder_minutes"]');
    if (calendarReminder && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('calendar_reminder_minutes', calendarReminder.value);
    }
    var openNextAfterRemove = e.target.closest('[data-open-next-after-remove-setting]');
    if (openNextAfterRemove && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('open_next_after_remove', openNextAfterRemove.checked ? 'true' : 'false');
    }
    var loadRemoteImages = e.target.closest('[data-load-remote-images-setting]');
    if (loadRemoteImages && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('load_remote_images', loadRemoteImages.checked ? 'true' : 'false');
    }
    var translationEnabled = e.target.closest('[data-translation-setting="enabled"]');
    if (translationEnabled && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('translation_button_enabled', translationEnabled.checked ? 'true' : 'false');
    }
    var translationProvider = e.target.closest('input[name="translation_provider"]');
    if (translationProvider && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('translation_provider', translationProvider.value);
    }
    var translationLanguage = e.target.closest('input[name="translation_target_language"]');
    if (translationLanguage && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('translation_target_language', translationLanguage.value);
    }
  });
  document.addEventListener('input', function(e) {
    var minChars = e.target.closest && e.target.closest('[data-compose-autosave-min-chars]');
    if (!minChars) return;
    minChars.value = minChars.value.replace(/\D/g, '').slice(0, 4);
  });
  ravenOnContent('[data-compose-autosave-conditions-value]', function() { syncComposeAutosaveControls(); });
})();

// Settings that only matter while a switch is on are marked
// data-setting-depends-on="<switch id>". They are turned off and faded with it.
(function() {
  function syncSettingDependents() {
    document.querySelectorAll('[data-setting-depends-on]').forEach(function(section) {
      var master = document.getElementById(section.getAttribute('data-setting-depends-on'));
      if (!master) return;
      // A switch that can't be used right now (notifications blocked for the
      // chosen method) leaves its settings open, so they can be changed.
      var enabled = master.checked || master.disabled;
      section.classList.toggle('opacity-50', !enabled);
      section.querySelectorAll('button, input:not([type="hidden"]), select, textarea').forEach(function(control) {
        control.disabled = !enabled;
      });
    });
  }
  window.ravenSyncSettingDependents = syncSettingDependents;
  ravenOnContent('[data-setting-depends-on]', function() { syncSettingDependents(); });
  document.addEventListener('change', function(e) {
    if (e.target && e.target.matches && e.target.matches('input[role="switch"]')) syncSettingDependents();
  });
})();

// Appearance tab
(function() {
  document.addEventListener('click', function(e) {
    var mode = e.target.closest('[data-mode]');
    if (mode) {
      if (typeof RavenSettings !== 'undefined') {
        RavenSettings.set('theme', mode.dataset.mode);
      }
      var isDark = mode.dataset.mode === 'system' ? document.documentElement.classList.contains('dark') : mode.dataset.mode === 'dark';
      document.querySelectorAll('[data-theme-preview]').forEach(function(el) {
        el.classList.toggle('dark', isDark && el.getAttribute('data-theme') !== 'express');
      });
      return;
    }
  });
  document.addEventListener('change', function(e) {
    var density = e.target.closest('input[name="mail_list_density"]');
    if (density && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('mail_list_density', density.value);
    }
    var appIcon = e.target.closest('input[name="app_icon"]');
    if (appIcon && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('app_icon', appIcon.value);
    }
    var themeStyle = e.target.closest('input[name="theme_style"]');
    if (themeStyle && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('theme_style', themeStyle.value);
    }
  });
})();

// Regional tab
(function() {
  function timezoneLabel(timezone) {
    if (!timezone || timezone === 'local') return 'Browser timezone';
    try {
      var now = new Date();
      var parts = new Intl.DateTimeFormat('en-US', { timeZone: timezone, timeZoneName: 'shortOffset' }).formatToParts(now);
      var offset = '';
      for (var i = 0; i < parts.length; i++) {
        if (parts[i].type === 'timeZoneName') offset = parts[i].value.replace('GMT', 'UTC');
      }
      return timezone.replace(/_/g, ' ') + (offset ? ' (' + offset + ')' : '');
    } catch (_) {
      return timezone;
    }
  }
  function browserTimezone() {
    try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch (_) { return 'UTC'; }
  }
  function timezoneOptions() {
    if (Intl.supportedValuesOf) {
      try { return Intl.supportedValuesOf('timeZone'); } catch (_) {}
    }
    return ['UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'Europe/Bucharest', 'Asia/Dubai', 'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Sydney'];
  }
  function populateTimezonePicker() {
    var root = document.querySelector('[data-timezone-picker]');
    if (!root) return;
    var content = root.querySelector('[data-timezone-options]');
    var group = content && content.querySelector('[role="group"]');
    var input = root.querySelector('[data-timezone-input]');
    if (!content || !group || !input) return;
    var current = root.getAttribute('data-current-timezone') || '';
    if (typeof RavenSettings !== 'undefined') current = RavenSettings.get('timezone') || current;
    if (!current || current === 'local') current = browserTimezone();
    var options = timezoneOptions();
    group.innerHTML = '';
    if (options.indexOf(current) === -1) options = [current].concat(options);
    for (var i = 0; i < options.length; i++) {
      var item = document.createElement('div');
      item.setAttribute('role', 'option');
      item.setAttribute('tabindex', '0');
      item.setAttribute('data-tui-selectbox-value', options[i]);
      item.setAttribute('data-tui-selectbox-selected', options[i] === current ? 'true' : 'false');
      item.setAttribute('data-tui-selectbox-disabled', 'false');
      item.className = 'select-item group relative flex w-full cursor-default select-none items-center rounded-sm py-1.5 px-2 text-sm font-light outline-none hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground data-[tui-selectbox-selected=true]:bg-accent data-[tui-selectbox-selected=true]:text-accent-foreground';
      var text = document.createElement('span');
      text.className = 'truncate select-item-text';
      text.textContent = timezoneLabel(options[i]);
      item.appendChild(text);
      group.appendChild(item);
    }
    input.value = current;
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }
  ravenOnContent('[data-timezone-picker]', function() { populateTimezonePicker(); });
  document.addEventListener('change', function(e) {
    var timezone = e.target.closest('[data-timezone-input]');
    if (timezone && typeof RavenSettings !== 'undefined') {
      var valueEl = timezone.closest('.select-trigger') && timezone.closest('.select-trigger').querySelector('.select-value');
      if (valueEl) valueEl.textContent = timezoneLabel(timezone.value);
      RavenSettings.set('timezone', timezone.value);
    }
  });
})();

// Advanced tab: switches saved as they change
(function() {
  document.addEventListener('change', function(e) {
    var toggle = e.target.closest('[data-ui-setting-switch]');
    if (!toggle || typeof RavenSettings === 'undefined') return;
    RavenSettings.set(toggle.getAttribute('data-ui-setting-switch'), toggle.checked ? 'true' : 'false');
  });
})();

// Contacts tab
(function() {
  function syncContactsAutoCreateDependents() {
    var master = document.querySelector('[data-contact-autocreate-switch]');
    var enabled = !master || master.checked;
    document.querySelectorAll('[data-contact-autocreate-dependent]').forEach(function(section) {
      section.disabled = !enabled;
      section.classList.toggle('opacity-45', !enabled);
      section.classList.toggle('pointer-events-none', !enabled);
      section.classList.toggle('saturate-50', !enabled);
    });
  }
  ravenOnContent('[data-contact-autocreate-switch]', function() { syncContactsAutoCreateDependents(); });
  document.addEventListener('change', function(e) {
    // The switch itself is saved by the advanced settings listener.
    var toggle = e.target.closest('[data-contact-autocreate-switch]');
    if (toggle) syncContactsAutoCreateDependents();
    var source = e.target.closest('[data-contact-source-switch]');
    if (!source || typeof RavenSettings === 'undefined') return;
    var selected = [];
    document.querySelectorAll('[data-contact-source-switch]').forEach(function(el) {
      if (el.checked) selected.push(el.getAttribute('data-contact-source-switch'));
    });
    RavenSettings.set('contacts_observed_sources', selected.join(','));
  });
})();

// Sync tab
(function() {
  var syncAutoSaveTimer = null;
  var syncSaveInFlight = false;
  var syncSaveQueued = false;

  function setPillEffectiveMode(pill, isIDLE) {
    var label = pill.querySelector('.sync-label');
    var toggle = pill.querySelector('[data-sync-pill-toggle]');
    pill.dataset.effectiveIdle = isIDLE ? 'true' : 'false';
    if (isIDLE) {
      pill.className = pill.className.replace(/bg-muted\/50 border-border text-muted-foreground/g, 'bg-amber-800/10 border-amber-700/20 text-amber-800 dark:bg-amber-300/10 dark:border-amber-300/20 dark:text-amber-200');
      if (!label && toggle) {
        label = document.createElement('span');
        label.className = 'sync-label text-xs opacity-70';
        toggle.appendChild(label);
      }
      if (label) label.textContent = 'real-time';
      return;
    }
    pill.className = pill.className.replace(/bg-amber-800\/10 border-amber-700\/20 text-amber-800 dark:bg-amber-300\/10 dark:border-amber-300\/20 dark:text-amber-200/g, 'bg-muted/50 border-border text-muted-foreground');
    if (label) label.remove();
  }

  function fallbackTooltipText(pill) {
    var reason = (pill.dataset.idleFallbackReason || '').trim().replace(/\.$/, '');
    if (!reason) return '';
    var name = pill.dataset.folderName || 'This folder';
    var message = name + ' was moved to polling because ' + reason + '.';
    var retryAt = Date.parse(pill.dataset.idleRetryAt || '');
    if (!isNaN(retryAt) && retryAt > Date.now()) {
      var remaining = retryAt - Date.now();
      var minutes = Math.max(1, Math.ceil(remaining / 60000));
      return message + ' Raven will try again in ' + minutes + (minutes === 1 ? ' minute.' : ' minutes.');
    }
    return message;
  }

  function setPillFallback(pill, reason, retryAt) {
    reason = (reason || '').trim();
    pill.dataset.idleFallbackReason = reason;
    pill.dataset.idleRetryAt = retryAt || '';
    var warning = pill.querySelector('[data-idle-fallback-warning]');
    if (warning) {
      warning.classList.toggle('hidden', !reason);
      warning.classList.toggle('inline-flex', !!reason);
    }
    var text = pill.querySelector('[data-idle-fallback-tooltip-text]');
    if (text) text.textContent = fallbackTooltipText(pill);
  }

  function movePill(pill, toIdle, animate, afterMove) {
    var container = pill.closest('[data-sync-account-card]');
    if (!container) return;
    var target = container.querySelector(toIdle ? '[data-idle-zone]' : '[data-poll-zone]');
    if (!target) return;
    var moved = pill.parentElement !== target;
    var complete = function() {
      setPillEffectiveMode(pill, toIdle);
      if (moved) target.appendChild(pill);
      if (animate && moved) {
        pill.classList.add('sync-pill-enter');
        pill.addEventListener('animationend', function onEnter() {
          pill.removeEventListener('animationend', onEnter);
          pill.classList.remove('sync-pill-enter');
        }, { once: true });
      }
      updateEmptyStates(container);
      if (afterMove) afterMove();
    };
    if (!animate || !moved) {
      complete();
      return;
    }
    pill.classList.add('sync-pill-exit');
    pill.addEventListener('animationend', function onExit() {
      pill.removeEventListener('animationend', onExit);
      pill.classList.remove('sync-pill-exit');
      complete();
    }, { once: true });
  }

  function togglePill(pill) {
    if (pill.classList.contains('sync-pill-exit')) return;
    var toIdle = pill.dataset.configuredIdle !== 'true';
    pill.dataset.configuredIdle = toIdle ? 'true' : 'false';
    setPillFallback(pill, '', '');
    movePill(pill, toIdle, true, function() {
      updateHiddenInputs();
      scheduleAutoSave(80);
    });
  }

  function applyIDLEFolderStatus(data) {
    if (!data || !data.account_id || !data.folder_id) return;
    var pill = Array.prototype.find.call(document.querySelectorAll('[data-sync-pill]'), function(candidate) {
      return candidate.dataset.accountId === data.account_id && candidate.dataset.folderId === data.folder_id;
    });
    if (!pill || pill.dataset.configuredIdle !== 'true') return;
    var effectiveIDLE = data.effective_mode === 'idle';
    setPillFallback(pill, effectiveIDLE ? '' : (data.reason || ''), effectiveIDLE ? '' : (data.retry_at || ''));
    movePill(pill, effectiveIDLE, true);
  }

  function updateEmptyStates(container) {
    var zones = container
      ? container.querySelectorAll('[data-idle-zone],[data-poll-zone]')
      : document.querySelectorAll('[data-idle-zone],[data-poll-zone]');
    zones.forEach(updateZoneEmpty);
  }

  function updateZoneEmpty(zone) {
    var pills = zone.querySelectorAll('[data-sync-pill]');
    var existing = zone.querySelector('.sync-empty-state');
    if (pills.length === 0 && !existing) {
      var el = document.createElement('span');
      el.className = 'sync-empty-state inline-flex items-center px-2.5 rounded-md border border-dashed border-border text-xs text-muted-foreground/50';
      el.style.height = '26px';
      el.textContent = 'None selected';
      zone.appendChild(el);
    } else if (pills.length > 0 && existing) {
      existing.remove();
    }
  }

  function updateHiddenInputs() {
    var container = document.getElementById('idle-folders-inputs');
    if (!container) return;
    container.innerHTML = '';
    var seenAccounts = {};
    var seenIdle = {};
    document.querySelectorAll('[data-sync-pill][data-configured-idle="true"]').forEach(function(pill) {
      var accountID = pill.dataset.accountId;
      var folderID = pill.dataset.folderId;
      var key = accountID + ':' + folderID;
      if (seenIdle[key]) return;
      seenIdle[key] = true;
      var input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'idle_folders';
      input.value = accountID + ':' + folderID;
      container.appendChild(input);
      seenAccounts[accountID] = true;
    });
    document.querySelectorAll('[data-poll-zone]').forEach(function(zone) {
      var accountID = zone.getAttribute('data-poll-zone');
      if (!seenAccounts[accountID]) {
        seenAccounts[accountID] = true;
      }
    });
    for (var aid in seenAccounts) {
      var input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'account_ids';
      input.value = aid;
      container.appendChild(input);
    }
  }

  document.addEventListener('click', function(e) {
    var toggle = e.target.closest('[data-sync-pill-toggle]');
    var pill = toggle && toggle.closest('[data-sync-pill]');
    if (pill && toggle) {
      e.preventDefault();
      togglePill(pill);
      return;
    }
  });

  document.addEventListener('raven:idle-folder-status', function(e) {
    applyIDLEFolderStatus(e.detail || {});
  });
  if (window.ravenIDLEFallbackTimer) window.clearInterval(window.ravenIDLEFallbackTimer);
  window.ravenIDLEFallbackTimer = window.setInterval(function() {
    document.querySelectorAll('[data-sync-pill][data-idle-fallback-reason]:not([data-idle-fallback-reason=""])').forEach(function(pill) {
      var text = pill.querySelector('[data-idle-fallback-tooltip-text]');
      if (text) text.textContent = fallbackTooltipText(pill);
    });
  }, 30000);

  function syncPollingIntervalDisplay() {
    var input = document.querySelector('#sync-settings-form input[name="sync_interval_minutes"]');
    if (!input || !input.value) return;
    var interval = input.value;
    document.querySelectorAll('[data-sync-polling-title]').forEach(function(title) {
      title.textContent = 'Polling (' + interval + ' min)';
    });
    document.querySelectorAll('[data-sync-folders]').forEach(function(container) {
      container.dataset.syncFolders = interval;
    });
    document.querySelectorAll('[data-sync-pill]').forEach(function(pill) {
      pill.dataset.interval = interval;
    });
  }

  function setSaveStatus(message) {
    var status = document.getElementById('sync-save-status');
    if (status) status.textContent = message || '';
  }

  function showSaveError() {
    if (typeof showRavenToast !== 'function') return;
    showRavenToast({
      id: 'sync-settings-toast',
      title: 'Could not save email sync settings',
      description: 'Try again in a moment.',
      variant: 'error',
      icon: 'error',
      position: 'bottom-right',
      duration: 5000,
      dismissible: true,
    });
  }

  function scheduleAutoSave(delay) {
    if (syncAutoSaveTimer) window.clearTimeout(syncAutoSaveTimer);
    syncAutoSaveTimer = window.setTimeout(saveSyncSettings, typeof delay === 'number' ? delay : 350);
  }

  function saveSyncSettings() {
    var form = document.getElementById('sync-settings-form');
    if (!form) return;
    if (syncSaveInFlight) {
      syncSaveQueued = true;
      return;
    }

    syncSaveInFlight = true;
    syncAutoSaveTimer = null;
    setSaveStatus('Saving');

    var formData = new FormData(form);
    var params = new URLSearchParams();
    formData.forEach(function(value, key) {
      params.append(key, value);
    });
    fetch('/api/settings/sync', {
      method: 'POST',
      body: params,
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json' }
    }).then(function(res) {
      if (!res.ok) throw new Error(res.statusText);
      return res.json();
    }).then(function() {
      setSaveStatus('Saved');
    }).catch(function() {
      setSaveStatus('Save failed');
      showSaveError();
    }).finally(function() {
      syncSaveInFlight = false;
      if (syncSaveQueued) {
        syncSaveQueued = false;
        scheduleAutoSave(0);
      }
    });
  }

  function syncUnifiedFolderControls() {
    var master = document.querySelector('[data-unified-folders-setting="enabled"]');
    var enabled = !master || master.checked;
    document.querySelectorAll('[data-unified-folder-switch]').forEach(function(input) {
      input.disabled = !enabled;
    });
    document.querySelectorAll('[data-unified-folder-account-switch]').forEach(function(input) {
      input.disabled = !enabled;
    });
    document.querySelectorAll('[data-unified-folder-account-button]').forEach(function(button) {
      button.disabled = !enabled;
    });
    document.querySelectorAll('[data-unified-folder-dependent]').forEach(function(row) {
      row.classList.toggle('opacity-50', !enabled);
    });
    document.querySelectorAll('[data-unified-folder-account-dependent]').forEach(function(row) {
      row.classList.toggle('opacity-50', !enabled);
    });
  }

  document.addEventListener('change', function(e) {
    var unifiedFolders = e.target.closest && e.target.closest('[data-unified-folders-setting="enabled"]');
    if (unifiedFolders && typeof RavenSettings !== 'undefined') {
      RavenSettings.set('unified_folders_enabled', unifiedFolders.checked ? 'true' : 'false');
      syncUnifiedFolderControls();
      return;
    }
    var unifiedFolder = e.target.closest && e.target.closest('[data-unified-folder-switch]');
    if (unifiedFolder && typeof RavenSettings !== 'undefined') {
      RavenSettings.set(unifiedFolder.name, unifiedFolder.checked ? 'true' : 'false');
      syncUnifiedFolderControls();
      return;
    }
    var unifiedFolderAccount = e.target.closest && e.target.closest('[data-unified-folder-account-switch]');
    if (unifiedFolderAccount && typeof RavenSettings !== 'undefined') {
      RavenSettings.set(unifiedFolderAccount.name, unifiedFolderAccount.checked ? 'true' : 'false');
    }
  });

  // The form's own listeners are bound each time the sync tab appears.
  ravenOnContent('#sync-settings-form', function(form) {
    form.addEventListener('submit', function(e) {
      e.preventDefault();
      saveSyncSettings();
    });
    form.addEventListener('change', function() {
      syncPollingIntervalDisplay();
      scheduleAutoSave();
    });
    form.addEventListener('input', function(e) {
      if (e.target && e.target.matches && e.target.matches('input[name="sync_interval_minutes"]')) {
        syncPollingIntervalDisplay();
        scheduleAutoSave();
      }
    });
    form.querySelector('[data-tui-selectbox-content]')?.addEventListener('click', function() {
      setTimeout(function() {
        syncPollingIntervalDisplay();
        scheduleAutoSave();
      }, 50);
    });

    updateHiddenInputs();
    updateEmptyStates();
    syncUnifiedFolderControls();
  });
})();

// Gmail account dialog
// The Gmail form signs in with the address as its username.
document.addEventListener('submit', function(e) {
  var form = e.target;
  if (!form || form.id !== 'gmail-form') return;
  var email = form.querySelector('input[name="email_address"]');
  var username = form.querySelector('input[name="username"]');
  if (email && username) username.value = email.value;
}, true);

// Edit account dialog
var _editWizStep = 0;
// Mail changes are kept in the form until step 1.d tests and saves them.
// The form counts as changed when its values differ from those it opened
// with: its widgets fire change events while loading, so events alone
// can't say whether the person changed anything.
var _editAccountSavedKey = null;
function editAccountFormKey() {
	var form = document.getElementById('edit-account-form');
	return form ? new URLSearchParams(new FormData(form)).toString() : '';
}
function editAccountChanged() {
	return _editAccountSavedKey !== null && editAccountFormKey() !== _editAccountSavedKey;
}
function editAccountMarkSaved() {
	_editAccountSavedKey = editAccountFormKey();
}
function newEditAccountTest() {
	return accountWizardTest({
		form: 'edit-account-form',
		results: 'edit-account-test-results',
		footer: '#edit-wizard-footer [data-wizard-footer-step="3"]',
		anywayLabel: 'Skip test and save',
		anywayFailedLabel: 'Save anyway',
		request: function(key) {
			var results = document.getElementById('edit-account-test-results');
			var typed = results && results.dataset.testTyped === 'true';
			return { url: results ? results.dataset.testUrl : '', body: typed ? key : '' };
		},
		onState: function() {
			var label = document.querySelector('#edit-wizard-footer [data-edit-account-save-label]');
			if (label) label.textContent = editAccountChanged() ? 'Save changes' : 'Continue';
		}
	});
}
var editAccountTest = newEditAccountTest();
// Each loaded edit dialog starts at its first step with a new test, and
// remembers its saved settings once its fields have been filled in.
// A button with data-edit-start-step (e.g. "Set up" next to Calendar Off) opens the edit
// dialog at that step instead of the first one.
var _editStartStep = 0;
document.addEventListener('click', function(e) {
	var opener = e.target && e.target.closest ? e.target.closest('[data-edit-start-step]') : null;
	if (opener) _editStartStep = parseInt(opener.getAttribute('data-edit-start-step'), 10) || 0;
}, true);
ravenOnContent('#edit-account-dialog', function() {
	_editWizStep = 0;
	_editAccountSavedKey = null;
	window.setTimeout(editAccountMarkSaved, 300);
	editAccountTest = newEditAccountTest();
	var start = _editStartStep;
	_editStartStep = 0;
	if (start) window.setTimeout(function() { editWizardGo(start, true); }, 0);
});
function editWizardGo(step, immediate) {
	if (step < 0) step = 0;
	if (step > 5) step = 5;
	var track = document.getElementById('edit-wizard-track');
	var root = document.getElementById('edit-account-dialog');
	if (!track || !root) return;
	// Moving on to contacts or calendar with unsaved mail changes would lose
	// them, so they go through the test step to be saved first.
	var unsaved = root.querySelector('[data-edit-account-unsaved]');
	if (step >= 4 && editAccountChanged()) {
		step = 3;
		if (unsaved) unsaved.hidden = false;
	} else if (unsaved) {
		unsaved.hidden = true;
	}
	_editWizStep = step;
	track.style.transform = 'translateX(-' + (step * (100 / 6)) + '%)';
	root.querySelectorAll('[data-account-wizard-step]').forEach(function(item) {
		// A rail item can stand for several slides (Server covers incoming and outgoing).
		item.dataset.active = String(+item.dataset.accountWizardStep === step || (item.dataset.accountWizardAlsoSteps || '').split(',').indexOf(String(step)) !== -1);
	});
	root.querySelectorAll('[data-wizard-section-steps]').forEach(function(section) {
		var steps = (section.dataset.wizardSectionSteps || '').split(',');
		section.dataset.active = String(steps.indexOf(String(step)) !== -1);
	});
	setWizardFooter(root, step, !!immediate);
	if (step === 3) editAccountTest.run(false);
}
// After a passing test: save the changes, or just continue when there are
// none, so the account isn't saved (and its sync restarted) for nothing.
function editAccountSaveOrContinue() {
	var form = document.getElementById('edit-account-form');
	if (!editAccountChanged() || !form) {
		editWizardGo(4, true);
		return;
	}
	form.requestSubmit();
}
function editAccountFormChanged(event) {
	if (!event.target || !event.target.closest || !event.target.closest('#edit-account-form')) return;
	editAccountTest.invalidate();
}
document.addEventListener('input', editAccountFormChanged);
document.addEventListener('change', editAccountFormChanged);
function editToggleSmtpAuth(checked) {
	var fields = document.getElementById('edit-smtp-auth-fields');
	var inputs = fields.querySelectorAll('input');
	fields.classList.toggle('opacity-50', checked);
	inputs.forEach(function(inp) { inp.disabled = checked; });
	if (checked) {
		var form = document.getElementById('edit-account-form');
		var u = form.querySelector('input[name="username"]');
		var p = form.querySelector('input[name="password"]');
		var su = form.querySelector('input[name="smtp_username"]');
		var sp = form.querySelector('input[name="smtp_password"]');
		if (su && u) su.value = u.value;
		if (sp && p) sp.value = p.value;
	}
}
