// Add/edit account wizard behaviour. The add dialog's markup is lazy (see
// AddAccountDialog in settings.templ), but this script is shared with the edit
// dialog, so it stays a static, cacheable asset.
var _wizStep = 0;
var _wizardFooterTimers = {};
function setWizardFooter(root, step, immediate) {
	if (!root) return;
	var footer = root.querySelector('.account-wizard-card-footer');
	if (!footer) return;
	root.querySelectorAll('[data-wizard-footer-step]').forEach(function(item) {
		if (item.parentElement !== footer) item.remove();
	});
	var key = root.id || 'wizard-footer';
	if (_wizardFooterTimers[key]) window.clearTimeout(_wizardFooterTimers[key]);
	footer.dataset.switching = immediate ? 'false' : 'true';
	function applyFooterStep() {
		var seen = {};
		Array.prototype.slice.call(footer.children).reverse().forEach(function(item) {
			if (!item || !item.hasAttribute || !item.hasAttribute('data-wizard-footer-step')) return;
			var itemStep = item.dataset.wizardFooterStep;
			if (seen[itemStep]) {
				item.remove();
				return;
			}
			seen[itemStep] = true;
			var active = +itemStep === +step;
			item.dataset.active = String(active);
			item.classList.toggle('hidden', !active);
		});
		footer.dataset.switching = 'false';
	}
	if (immediate) {
		applyFooterStep();
		return;
	}
	_wizardFooterTimers[key] = window.setTimeout(applyFooterStep, 180);
}
function wizardGo(step, immediate) {
	var track = document.getElementById('wizard-track');
	var root = document.getElementById('add-account-dialog');
	if (!track || !root) return;
	if (step < 0) step = 0;
	if (step > 5) step = 5;
	syncAddContactStep();
	// The Email steps end by creating the account. Going back to them would
	// create it again, so after that its settings belong to Edit account.
	if (step <= 3 && addAccountCreated()) return;
	if (step === 3) {
		var form = document.getElementById('account-form');
		if (!form || !form.reportValidity()) return;
	}
	if (step >= 4 && !addAccountCreated()) return;
	if (step === 4 && !addContactsEnabled()) step = 5;
	_wizStep = step;
	track.style.transform = 'translateX(-' + (step * (100 / 6)) + '%)';
	root.querySelectorAll('[data-account-wizard-step]').forEach(function(item) {
		item.dataset.active = String(+item.dataset.accountWizardStep === step);
	});
	root.querySelectorAll('[data-wizard-section-steps]').forEach(function(section) {
		var steps = (section.dataset.wizardSectionSteps || '').split(',');
		section.dataset.active = String(steps.indexOf(String(step)) !== -1);
	});
	setWizardFooter(root, step, !!immediate);
	if (step === 3) addAccountTest.run(false);
}
function addContactsEnabled() {
	var toggle = document.querySelector('[data-add-contact-sync-switch]');
	return !toggle || toggle.checked;
}
function addAccountFormReady() {
	var form = document.getElementById('account-form');
	return !!(form && form.checkValidity());
}
function addAccountCreated() {
	return !!document.querySelector('#add-contacts-content [data-add-account-created]');
}
function syncAddAccountSubmitButtons() {
	var disabled = !addAccountFormReady();
	document.querySelectorAll('#add-account-dialog [data-add-account-submit-button]').forEach(function(button) {
		button.disabled = disabled;
	});
}
// Both account wizards end their mail steps with step 1.d, which tests the
// typed settings before the account is created or saved. Its footer follows
// the test: while it runs, the account can be saved without waiting; once it
// passes, saved; once it fails, the settings can be fixed (Back), tested
// again, or saved anyway. Coming back with the same settings keeps the
// earlier result instead of testing again.
function accountWizardTest(options) {
	var test = { state: 'idle', key: '' };
	function results() { return document.getElementById(options.results); }
	function spinner(message) {
		var row = document.createElement('div');
		row.className = 'flex items-center gap-2 py-1';
		row.innerHTML = '<div class="size-4 rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground animate-spin"></div><span class="text-xs text-muted-foreground"></span>';
		row.querySelector('span').textContent = message;
		return row;
	}
	function setState(state) {
		test.state = state;
		var footer = document.querySelector(options.footer);
		if (!footer) return;
		// The hidden attribute, not a class: these buttons are inline-flex,
		// which would win over a "hidden" utility class.
		// Once created or saved, the person can close the window here or go on
		// to contacts and calendar.
		var done = state === 'done';
		var anyway = footer.querySelector('[data-account-test-anyway]');
		var retest = footer.querySelector('[data-account-test-retest]');
		var save = footer.querySelector('[data-account-test-save]');
		var back = footer.querySelector('[data-account-test-back]');
		if (anyway) {
			anyway.textContent = state === 'failed' ? options.anywayFailedLabel : options.anywayLabel;
			anyway.hidden = state === 'passed' || done;
		}
		if (retest) retest.hidden = state !== 'failed';
		if (save) save.hidden = state !== 'passed';
		if (back && options.lockWhenDone) back.hidden = done;
		footer.querySelectorAll('[data-account-test-close], [data-account-test-continue]').forEach(function(button) {
			button.hidden = !done;
		});
		footer.querySelectorAll('[data-account-test-action]').forEach(function(button) {
			button.disabled = state === 'saving';
		});
		if (options.onState) options.onState(state);
	}
	function showError(message) {
		var box = results();
		if (!box) return;
		var alert = document.createElement('div');
		alert.setAttribute('role', 'alert');
		alert.className = 'rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive';
		alert.textContent = message;
		box.textContent = '';
		box.appendChild(alert);
	}
	function clearSaving() {
		var box = results();
		var row = box && box.querySelector('[data-account-test-saving]');
		if (row) row.remove();
	}
	return {
		state: function() { return test.state; },
		run: function(force) {
			var form = document.getElementById(options.form);
			if (!form) return;
			var key = new URLSearchParams(new FormData(form)).toString();
			var current = test.state === 'passed' || test.state === 'failed' || test.state === 'testing';
			if (!force && key === test.key && current) return;
			test.key = key;
			setState('testing');
			var box = results();
			if (box) {
				box.textContent = '';
				box.appendChild(spinner('Testing connection...'));
			}
			var request = options.request(key);
			fetch(request.url, {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'text/html' },
				body: request.body
			}).then(function(response) {
				return response.text().then(function(body) {
					if (test.key !== key || test.state !== 'testing') return;
					if (!response.ok) throw new Error((body || '').trim() || 'Could not test the connection.');
					var box = results();
					if (box) box.innerHTML = body;
					setState(response.headers.get('X-Raven-Connection-Test') === 'ok' ? 'passed' : 'failed');
				});
			}).catch(function(error) {
				if (test.key !== key || test.state !== 'testing') return;
				showError(error && error.message ? error.message : 'Could not test the connection.');
				setState('failed');
			});
		},
		saving: function(message) {
			setState('saving');
			var box = results();
			if (!box) return;
			clearSaving();
			var row = spinner(message);
			row.setAttribute('data-account-test-saving', '');
			box.appendChild(row);
		},
		saved: function(message) {
			clearSaving();
			var box = results();
			if (box && message) {
				var note = document.createElement('div');
				note.setAttribute('role', 'status');
				note.className = 'rounded-md border border-border bg-muted/35 px-3 py-2 text-xs leading-relaxed text-foreground';
				note.textContent = message;
				box.appendChild(note);
			}
			setState('done');
		},
		saveFailed: function(message) {
			clearSaving();
			if (message) showError(message);
			setState('failed');
		},
		// Changing a setting makes an earlier result stale.
		invalidate: function() {
			if (test.state === 'saving' || test.state === 'done') return;
			test.key = '';
			test.state = 'idle';
		}
	};
}
function newAddAccountTest() {
	return accountWizardTest({
		form: 'account-form',
		results: 'add-account-test-results',
		footer: '#add-wizard-footer [data-wizard-footer-step="3"]',
		anywayLabel: 'Skip test and create',
		anywayFailedLabel: 'Create anyway',
		// Going back would create the account a second time.
		lockWhenDone: true,
		request: function(key) { return { url: '/api/accounts/test', body: key }; }
	});
}
var addAccountTest = newAddAccountTest();
// A newly rendered dialog starts at its first step with a new test.
ravenOnContent('#add-account-dialog', function() {
	_wizStep = 0;
	_wizardFooterTimers = {};
	addAccountTest = newAddAccountTest();
});
document.body.addEventListener('htmx:beforeRequest', function(event) {
	if (!event.target || event.target.id !== 'account-form') return;
	addAccountTest.saving('Creating account...');
});
// Creating failed: say why on the test step, where the person is. Managed
// servers answer with an error status; others send an error card in place
// of the next steps.
document.body.addEventListener('htmx:afterRequest', function(event) {
	if (!event.target || event.target.id !== 'account-form') return;
	if (event.detail && event.detail.successful) {
		if (!addAccountCreated() && addAccountTest.state() === 'saving') addAccountTest.saveFailed('');
		return;
	}
	var xhr = event.detail && event.detail.xhr;
	addAccountTest.saveFailed(xhr && xhr.responseText ? xhr.responseText.trim() : 'Could not create the account.');
});
document.body.addEventListener('htmx:afterSwap', function(event) {
	var content = event.target;
	if (!content || content.id !== 'add-contacts-content' || addAccountCreated()) return;
	var errorCard = content.querySelector('[data-account-form-error]');
	if (!errorCard) return;
	var message = errorCard.querySelector('p');
	content.textContent = '';
	addAccountTest.saveFailed(message ? message.textContent : 'Could not create the account.');
});
document.addEventListener('input', function(event) {
	if (!event.target || !event.target.closest || !event.target.closest('#account-form') || addAccountCreated()) return;
	addAccountTest.invalidate();
});
function setWizardStepDisabled(step, disabled) {
	if (!step) return;
	step.disabled = !!disabled;
	var section = step.closest ? step.closest('.account-wizard-section') : null;
	if (section) section.dataset.disabled = String(!!disabled);
}
function syncAddContactStep() {
	var root = document.getElementById('add-account-dialog');
	if (!root) return;
	var contactStep = root.querySelector('[data-account-wizard-step="4"]');
	var calendarStep = root.querySelector('[data-account-wizard-step="5"]');
	var created = addAccountCreated();
	root.querySelectorAll('[data-wizard-section-steps="0,1,2,3"] [data-account-wizard-step], [data-wizard-section-steps="0,1,2,3"] .account-wizard-section-title').forEach(function(button) {
		button.disabled = created;
	});
	setWizardStepDisabled(contactStep, !created || !addContactsEnabled());
	setWizardStepDisabled(calendarStep, !created);
	syncAddAccountSubmitButtons();
}
// Runs after the contacts step of the add dialog has been loaded.
function syncAddContactStepAfterSwap() {
	syncAddContactStep();
	maybeGoAddContacts();
}
function maybeGoAddContacts() {
	if (!addAccountCreated()) return;
	syncAddContactStep();
	addAccountTest.saved('Account created. Mail is syncing now. You can close this window, or continue to set up this account\'s contacts and calendar.');
}
function syncWizardServiceContent(root) {
	root = root || document;
	root.querySelectorAll('[data-wizard-service-content]').forEach(function(content) {
		var service = content.dataset.wizardServiceContent;
		var toggle = root.querySelector('[data-wizard-service-switch="' + service + '"]');
		var alwaysActive = content.dataset.wizardServiceAlwaysActive === 'true';
		var enabled = alwaysActive || !toggle || toggle.checked;
		content.classList.toggle('opacity-50', !enabled);
		content.classList.toggle('pointer-events-none', !enabled);
		content.classList.toggle('select-none', !enabled);
		content.setAttribute('aria-disabled', String(!enabled));
		content.querySelectorAll('input, select, textarea, button').forEach(function(el) {
			if (!el.hasAttribute('data-wizard-original-tabindex')) el.setAttribute('data-wizard-original-tabindex', el.getAttribute('tabindex') || '');
			if (enabled) {
				var original = el.getAttribute('data-wizard-original-tabindex');
				if (original === '') el.removeAttribute('tabindex'); else el.setAttribute('tabindex', original);
			} else {
				el.setAttribute('tabindex', '-1');
			}
		});
	});
}
function wizardServiceStatus(root, service, message) {
	var status = root.querySelector('[data-wizard-service-status="' + service + '"]');
	if (!status) return;
	status.textContent = '';
	var messageBox = document.createElement('div');
	messageBox.className = 'rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive';
	messageBox.textContent = message;
	status.appendChild(messageBox);
}
// Calendar is the last step of both wizards: moving on from it finishes.
function wizardServiceAdvance(root, step) {
	if (root.id === 'add-account-dialog') {
		if (step === 'done' || (step >= 5 && _wizStep === 5)) {
			window.location.href = '/settings/accounts?account_added=1';
			return;
		}
		wizardGo(step);
	} else if (typeof editWizardGo === 'function') {
		if (step === 'done' || (step >= 5 && _editWizStep === 5)) {
			window.location.href = '/settings/accounts';
			return;
		}
		editWizardGo(step, true);
	}
}
function wizardServiceNext(button, service, step) {
	var root = button && button.closest ? button.closest('#add-account-dialog,#edit-account-dialog') : null;
	if (!root) return;
	var toggle = root.querySelector('[data-wizard-service-switch="' + service + '"]');
	var enabled = !toggle || toggle.checked;
	var contactForm = service === 'contacts' ? root.querySelector('[data-wizard-contact-form]') : null;
	var calendarForm = service === 'calendar' && enabled ? root.querySelector('[data-calendar-source-form]') : null;
	if (enabled && contactForm) {
		if (contactForm.requestSubmit) contactForm.requestSubmit(); else contactForm.submit();
		return;
	}
	if (enabled && calendarForm) {
		if (calendarForm.requestSubmit) calendarForm.requestSubmit(); else calendarForm.submit();
		return;
	}
	// Calendar can be toggled on before discovery so users can inspect the
	// setup. With no discovered sources there is nothing valid to persist;
	// let the wizard continue and leave that transient toggle unsaved.
	if (service === 'calendar' && !root.querySelector('[data-calendar-source-checkbox]')) {
		wizardServiceAdvance(root, step);
		return;
	}
	var serviceForm = toggle && toggle.closest ? toggle.closest('[data-wizard-service-form]') : null;
	var accountID = serviceForm ? serviceForm.dataset.accountId : '';
	var initialEnabled = toggle ? toggle.dataset.wizardServiceInitialEnabled : '';
	if (!accountID || !toggle || String(enabled) === initialEnabled || toggle.disabled) {
		wizardServiceAdvance(root, step);
		return;
	}
	var wasDisabled = toggle.disabled;
	toggle.disabled = true;
	wizardServiceStatus(root, service, 'Saving ' + service + ' settings...');
	var body = new URLSearchParams();
	body.append('service', service);
	body.append('enabled', enabled ? 'true' : 'false');
	fetch('/api/accounts/' + encodeURIComponent(accountID) + '/services', {
		method: 'POST',
		credentials: 'same-origin',
		headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'text/html' },
		body: body.toString()
	}).then(function(response) {
	if (!response.ok) {
		return response.text().then(function(message) {
			throw new Error((message || '').trim() || 'Could not save ' + service + ' settings.');
		});
	}
		toggle.dataset.wizardServiceInitialEnabled = String(enabled);
		wizardServiceAdvance(root, step);
	}).catch(function(error) {
		toggle.checked = initialEnabled === 'true';
		wizardServiceStatus(root, service, error.message || 'Could not save ' + service + ' settings.');
	}).finally(function() {
		toggle.disabled = wasDisabled;
	});
}
document.addEventListener('click', function(event) {
	var button = event.target && event.target.closest ? event.target.closest('[data-account-wizard-action]') : null;
	if (!button) return;
	if (button.hasAttribute('onclick')) return;
	var step = Number(button.dataset.accountWizardTargetStep || 0);
	if (button.dataset.accountWizardAction === 'wizardGo') wizardGo(step);
	if (button.dataset.accountWizardAction === 'editWizardGo' && typeof editWizardGo === 'function') editWizardGo(step);
});
document.addEventListener('change', function(event) {
	if (event.target && event.target.matches && event.target.matches('[data-add-contact-sync-switch]')) {
		syncAddContactStep();
		if (_wizStep === 4 && !addContactsEnabled()) wizardGo(5);
	}
	if (event.target && event.target.closest && event.target.closest('#account-form')) syncAddContactStep();
	if (event.target && event.target.matches && event.target.matches('[data-wizard-service-switch]')) {
		var root = event.target.closest('#add-account-dialog,#edit-account-dialog') || document;
		syncWizardServiceContent(root);
	}
});
function toggleSmtpAuth(checked) {
	var fields = document.getElementById('smtp-auth-fields');
	var inputs = fields.querySelectorAll('input');
	fields.classList.toggle('opacity-50', checked);
	inputs.forEach(function(inp) { inp.disabled = checked; });
	if (checked) {
		var u = document.querySelector('input[name="username"]');
		var p = document.querySelector('input[name="password"]');
		var su = document.querySelector('input[name="smtp_username"]');
		var sp = document.querySelector('input[name="smtp_password"]');
		if (su && u) su.value = u.value;
		if (sp && p) sp.value = p.value;
	}
}
function mailDiscoveryStatus() {
	return document.getElementById('mail-discovery-status');
}
function mailDiscoveryEscape(value) {
	return String(value || '').replace(/[&<>"]/g, function(ch) {
		return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[ch]);
	});
}
function mailDiscoverySourceLabel(source) {
	if (source === 'preset') return 'Built-in preset';
	if (source === 'provider_xml') return 'Provider XML';
	if (source === 'thunderbird_xml') return 'Thunderbird XML';
	if (source === 'mx_provider_xml') return 'Provider via MX';
	if (source === 'dns_srv') return 'DNS SRV';
	if (source === 'heuristic') return 'Common hostnames';
	return source || 'Discovery';
}
function mailDiscoveryProviderLabel(provider) {
	if (provider === 'gmail') return 'Google';
	if (provider === 'outlook') return 'Microsoft';
	return 'provider';
}
// iCloud preset: values from https://support.apple.com/en-us/102525
function openICloudAccountDialog() {
	var form = document.getElementById('account-form');
	if (!form) {
		// The form arrives with the lazily fetched wizard markup.
		loadAddAccountDialog().then(function() {
			if (document.getElementById('account-form')) openICloudAccountDialog();
		});
		return;
	}
	mailDiscoverySetField(form, 'imap_host', 'imap.mail.me.com');
	mailDiscoverySetField(form, 'imap_port', '993');
	mailDiscoverySetField(form, 'imap_tls_mode', 'tls');
	mailDiscoverySetField(form, 'smtp_host', 'smtp.mail.me.com');
	mailDiscoverySetField(form, 'smtp_port', '587');
	mailDiscoverySetField(form, 'smtp_tls_mode', 'starttls');
	mailDiscoverySetField(form, 'auth_method', 'plain');
	var sameAuth = document.getElementById('smtp-same-auth');
	if (sameAuth) {
		sameAuth.checked = true;
		toggleSmtpAuth(true);
	}
	var email = form.querySelector('[name="email_address"]');
	var username = form.querySelector('[name="username"]');
	var host = form.querySelector('[name="imap_host"]');
	var hint = document.getElementById('icloud-password-hint');
	function isICloud() { return host && host.value === 'imap.mail.me.com'; }
	if (email && username && host && form.dataset.icloudBound !== 'true') {
		form.dataset.icloudBound = 'true';
		// Apple's SMTP needs the full address, so use it as the shared login.
		email.addEventListener('input', function() {
			if (!isICloud() || (username.value && username.value !== (form.dataset.icloudSynced || ''))) return;
			form.dataset.icloudSynced = email.value.trim();
			mailDiscoverySetField(form, 'username', form.dataset.icloudSynced);
		});
		host.addEventListener('input', function() {
			if (hint) hint.classList.toggle('hidden', !isICloud());
		});
	}
	if (email && username && email.value.trim()) {
		form.dataset.icloudSynced = email.value.trim();
		mailDiscoverySetField(form, 'username', form.dataset.icloudSynced);
	}
	if (hint) hint.classList.remove('hidden');
	bindAddWizardObserver();
	if (window.tui && window.tui.dialog) window.tui.dialog.open('add-account-dialog');
}
function mailDiscoverySetField(form, name, value) {
	var input = form.querySelector('[name="' + name + '"]');
	if (!input) return;
	input.value = value || '';
	input.dispatchEvent(new Event('input', { bubbles: true }));
	input.dispatchEvent(new Event('change', { bubbles: true }));
}
function setupMailDiscoveryOAuthSubmit(form) {
	if (!form || form.dataset.mailDiscoveryOAuthBound === 'true') return;
	form.dataset.mailDiscoveryOAuthBound = 'true';
	form.addEventListener('submit', function(event) {
		var provider = form.dataset.discoveryOauthProvider || '';
		if (!provider) return;
		event.preventDefault();
		event.stopImmediatePropagation();
		var oauthForm = document.createElement('form');
		oauthForm.method = 'POST';
		oauthForm.action = '/api/accounts/oauth2/authorize';
		oauthForm.style.display = 'none';
		var data = new FormData(form);
	data.set('provider', provider);
	data.set('flow_action', 'add');
		data.set('auth_method', 'oauth2');
		data.set('password', '_oauth2_');
		data.forEach(function(value, key) {
			var input = document.createElement('input');
			input.type = 'hidden';
			input.name = key;
			input.value = value;
			oauthForm.appendChild(input);
		});
		document.body.appendChild(oauthForm);
		oauthForm.submit();
	}, true);
}
function mailDiscoverySetOAuthMode(form, candidate) {
	var provider = candidate && candidate.auth_method === 'oauth2' ? (candidate.provider || '') : '';
	var password = form ? form.querySelector('[name="password"]') : null;
	if (provider) {
		form.dataset.discoveryOauthProvider = provider;
		if (password) {
			password.required = false;
			password.value = '_oauth2_';
		}
		setupMailDiscoveryOAuthSubmit(form);
		return;
	}
	if (form) delete form.dataset.discoveryOauthProvider;
	if (password) {
		password.required = true;
		if (password.value === '_oauth2_') password.value = '';
	}
}
function applyMailDiscoveryCandidate(index) {
	var candidates = window._mailDiscoveryCandidates || [];
	var candidate = candidates[index];
	var form = document.getElementById('account-form');
	var status = mailDiscoveryStatus();
	if (!candidate || !form) return;
	mailDiscoverySetField(form, 'imap_host', candidate.imap_host || '');
	mailDiscoverySetField(form, 'imap_port', candidate.imap_port || '');
	mailDiscoverySetField(form, 'imap_tls_mode', candidate.imap_tls_mode || 'tls');
	mailDiscoverySetField(form, 'smtp_host', candidate.smtp_host || '');
	mailDiscoverySetField(form, 'smtp_port', candidate.smtp_port || '');
	mailDiscoverySetField(form, 'smtp_tls_mode', candidate.smtp_tls_mode || 'tls');
	mailDiscoverySetField(form, 'username', candidate.username || '');
	mailDiscoverySetField(form, 'auth_method', candidate.auth_method || 'plain');
	mailDiscoverySetOAuthMode(form, candidate);
	var sameAuth = document.getElementById('smtp-same-auth');
	var separateSmtpUser = candidate.smtp_username && candidate.smtp_username !== candidate.username;
	if (sameAuth) {
		sameAuth.checked = !separateSmtpUser;
		toggleSmtpAuth(sameAuth.checked);
	}
	if (separateSmtpUser) mailDiscoverySetField(form, 'smtp_username', candidate.smtp_username || '');
	if (status) {
		var label = mailDiscoverySourceLabel(candidate.source);
		var summary = mailDiscoveryEscape(candidate.imap_host) + ':' + mailDiscoveryEscape(candidate.imap_port) + ' / ' + mailDiscoveryEscape(candidate.smtp_host) + ':' + mailDiscoveryEscape(candidate.smtp_port);
		status.querySelectorAll('[data-mail-discovery-candidate]').forEach(function(item, idx) {
			item.classList.toggle('border-primary', idx === index);
			item.classList.toggle('bg-primary/10', idx === index);
		});
		var applied = status.querySelector('[data-mail-discovery-applied]');
		if (applied) {
			var oauthHint = candidate.auth_method === 'oauth2' && candidate.provider ? ' Continue with ' + mailDiscoveryProviderLabel(candidate.provider) + ' sign-in to finish.' : '';
			applied.textContent = 'Applied ' + label + ' settings: ' + summary + oauthHint;
		}
	}
}
function renderMailDiscoveryCandidates(candidates) {
	var status = mailDiscoveryStatus();
	if (!status) return;
	if (!candidates.length) {
		status.innerHTML = '<div class="rounded-md border border-border bg-background/60 px-3 py-2 text-xs text-muted-foreground">No automatic settings were found. Continue with manual setup.</div>';
		return;
	}
	var html = '<div class="rounded-lg border border-border bg-background/60 p-3 text-xs"><div class="flex items-start justify-between gap-3"><div><div class="font-semibold text-foreground" data-mail-discovery-applied>Choose a configuration to apply</div><div class="mt-0.5 text-muted-foreground">' + candidates.length + ' candidate' + (candidates.length === 1 ? '' : 's') + ' found</div></div></div>';
	html += '<div class="mt-3 grid gap-2">';
	candidates.forEach(function(candidate, index) {
		var title = mailDiscoverySourceLabel(candidate.source);
		var summary = mailDiscoveryEscape(candidate.imap_host) + ':' + mailDiscoveryEscape(candidate.imap_port) + ' ' + mailDiscoveryEscape(candidate.imap_tls_mode) + ' / ' + mailDiscoveryEscape(candidate.smtp_host) + ':' + mailDiscoveryEscape(candidate.smtp_port) + ' ' + mailDiscoveryEscape(candidate.smtp_tls_mode);
		var notes = Array.isArray(candidate.notes) ? candidate.notes.filter(Boolean).slice(0, 2) : [];
		var card = '<button type="button" data-mail-discovery-candidate class="w-full rounded-md border border-border bg-card px-3 py-2 text-left transition-colors hover:bg-accent" data-on-click=\'["applyMailDiscoveryCandidate",' + index + ']\'><span class="flex items-center justify-between gap-3"><span class="font-semibold text-foreground">' + mailDiscoveryEscape(title) + '</span><span class="text-xs text-muted-foreground">' + mailDiscoveryEscape(candidate.confidence) + '%</span></span><span class="mt-1 block truncate text-muted-foreground">' + summary + '</span>';
		if (notes.length) {
			card += '<span class="mt-1 block text-xs leading-relaxed text-muted-foreground">' + mailDiscoveryEscape(notes.join(' ')) + '</span>';
		}
		html += card + '</button>';
	});
	html += '</div>';
	html += '</div>';
	status.innerHTML = html;
}
function discoverMailSettings(button) {
	var form = document.getElementById('account-form');
	var status = mailDiscoveryStatus();
	if (!form || !status) return;
	var email = form.querySelector('input[name="email_address"]');
	if (!email || !email.value.trim()) {
		status.innerHTML = '<div class="rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">Enter an email address first.</div>';
		return;
	}
	var label = button ? button.querySelector('[data-mail-discovery-label]') : null;
	var originalLabel = label ? label.textContent : '';
	if (button) button.disabled = true;
	if (label) label.textContent = 'Discovering...';
	status.innerHTML = '<div class="rounded-lg border border-border bg-background/60 p-3 text-xs text-muted-foreground"><div class="flex items-center gap-2"><span class="size-3.5 rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground animate-spin"></span><span>Checking provider XML, MX provider records, DNS SRV, and verified common hostnames...</span></div></div>';
	var body = new URLSearchParams();
	body.append('email_address', email.value.trim());
	fetch('/api/accounts/discover', {
		method: 'POST',
		credentials: 'same-origin',
		headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json' },
		body: body.toString()
	}).then(function(resp) {
		return resp.json().then(function(data) {
			if (!resp.ok) throw new Error(data && data.error ? data.error : 'Could not discover settings.');
			return data;
		});
	}).then(function(data) {
		window._mailDiscoveryCandidates = data.candidates || [];
		renderMailDiscoveryCandidates(window._mailDiscoveryCandidates);
	}).catch(function(err) {
		status.innerHTML = '<div class="rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">' + mailDiscoveryEscape(err && err.message ? err.message : 'Could not discover settings.') + '</div>';
	}).finally(function() {
		if (button) button.disabled = false;
		if (label) label.textContent = originalLabel || 'Discover settings';
	});
}
function contactSyncSaveButtons(accountID) {
	return document.querySelectorAll('[data-contact-sync-save-button][data-account-id="' + accountID + '"]');
}
function contactSyncSetSaveButton(accountID, label, disabled, continueMode) {
	contactSyncSaveButtons(accountID).forEach(function(button) {
		var buttonLabel = button.querySelector('[data-account-edit-submit-label]');
		if (buttonLabel) buttonLabel.textContent = label; else button.textContent = label;
		button.disabled = !!disabled;
		button.setAttribute('aria-busy', label === 'Saving...' ? 'true' : 'false');
		var buttonSpinner = button.querySelector('[data-account-edit-submit-spinner]');
		if (buttonSpinner) buttonSpinner.classList.toggle('hidden', label !== 'Saving...');
		if (continueMode) {
			button.type = 'button';
			button.removeAttribute('form');
			button.onclick = function() { document.getElementById('edit-account-form').requestSubmit(); };
		} else {
			button.type = 'submit';
			button.setAttribute('form', 'account-contact-sync-form-' + accountID);
			button.onclick = null;
		}
	});
}
function contactSyncSaving(accountID) {
	contactSyncSetSaveButton(accountID, 'Saving...', true, false);
}
function contactSyncSaved(accountID, ok) {
	if (!ok) {
		contactSyncSetSaveButton(accountID, 'Save', false, false);
		return;
	}
	contactSyncSetSaveButton(accountID, 'Success', true, false);
	window.setTimeout(function() {
		contactSyncSetSaveButton(accountID, 'Continue', false, true);
	}, 2500);
}
function contactSyncResetSave(accountID) {
	contactSyncSetSaveButton(accountID, 'Save', false, false);
}
function toggleCardDAVAuth(checkbox) {
	var form = checkbox && checkbox.closest ? checkbox.closest('form') : null;
	if (!form) return;
	var fields = form.querySelector('[data-carddav-credential-fields]');
	if (!fields) return;
	fields.classList.toggle('opacity-50', checkbox.checked);
	fields.querySelectorAll('input[name="username"], input[name="password"]').forEach(function(input) {
		input.disabled = checkbox.checked;
	});
}
function discoverCardDAV(accountID, fieldsID, statusID, autodiscover) {
	var fields = document.getElementById(fieldsID);
	var status = document.getElementById(statusID);
	if (!fields || !status) return;
	var controller = window.AbortController ? new AbortController() : null;
	var timeout = controller ? window.setTimeout(function() { controller.abort(); }, 28000) : null;
	var body = new URLSearchParams();
	fields.querySelectorAll('input, select, textarea').forEach(function(input) {
		if (!input.name || input.disabled) return;
		body.append(input.name, input.value || '');
	});
	if (autodiscover) body.append('autodiscover', '1');
	function renderDiscoveryProgress(completed, total, endpoint) {
		var percent = total > 0 ? Math.max(0, Math.min(100, Math.round((completed / total) * 100))) : 0;
		var label = total > 0 ? 'Testing ' + Math.min(completed + 1, total) + ' of ' + total + ' CardDAV checks' : 'Preparing CardDAV discovery...';
		var safeEndpoint = endpoint ? endpoint.replace(/[&<>"]/g, function(ch) { return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[ch]); }) : '';
		status.innerHTML = '<div class="rounded-lg border border-border bg-background/60 p-3 text-xs text-muted-foreground"><div class="flex items-center justify-between gap-3"><span class="font-medium text-foreground">' + label + '</span><span class="tabular-nums">' + completed + '/' + (total || '?') + '</span></div><div class="mt-2 h-1.5 overflow-hidden rounded-md bg-muted"><div class="h-full rounded-md bg-primary transition-all" style="width:' + percent + '%"></div></div>' + (safeEndpoint ? '<div class="mt-2 truncate">' + safeEndpoint + '</div>' : '') + '</div>';
	}
	renderDiscoveryProgress(0, 0, '');
	fetch('/api/accounts/' + encodeURIComponent(accountID) + '/contacts/sync/discover', {
		method: 'POST',
		credentials: 'same-origin',
		headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/x-ndjson' },
		body: body.toString(),
		signal: controller ? controller.signal : undefined
	}).then(function(resp) {
		if (!resp.body || !resp.body.getReader) throw new Error('stream unavailable');
		var reader = resp.body.getReader();
		var decoder = new TextDecoder();
		var buffer = '';
		function handleEvent(event) {
			if (!event || !event.type) return;
			if (event.type === 'progress') {
				renderDiscoveryProgress(event.completed || 0, event.total || 0, event.endpoint || '');
				return;
			}
			if (event.type === 'error') {
				if (timeout) window.clearTimeout(timeout);
				var err = document.createElement('div');
				err.className = 'rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive';
				err.textContent = event.error || 'Could not discover CardDAV address books.';
				status.textContent = '';
				status.appendChild(err);
				return;
			}
			if (event.type === 'done') {
				if (timeout) window.clearTimeout(timeout);
				renderDiscoveryResults(event.address_books || []);
			}
		}
		function read() {
			return reader.read().then(function(result) {
				if (result.done) return;
				buffer += decoder.decode(result.value, { stream: true });
				var lines = buffer.split('\n');
				buffer = lines.pop() || '';
				lines.forEach(function(line) {
					line = line.trim();
					if (!line) return;
					try { handleEvent(JSON.parse(line)); } catch (_) {}
				});
				return read();
			});
		}
		return read();
	}).catch(function(err) {
		if (timeout) window.clearTimeout(timeout);
		var message = err && err.name === 'AbortError' ? 'CardDAV discovery timed out. Check the app logs for the attempted DNS and URL checks, or enter the provider\'s exact CardDAV base URL and try again.' : 'Could not discover CardDAV address books. Check the app logs for details.';
		status.innerHTML = '<div class="rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">' + message + '</div>';
	});
	function renderDiscoveryResults(books) {
		status.textContent = '';
		if (!books.length) {
			var empty = document.createElement('div');
			empty.className = 'rounded-md border border-border bg-background/60 px-3 py-2 text-xs text-muted-foreground';
			empty.textContent = 'No CardDAV address books were found.';
			status.appendChild(empty);
			return;
		}
		var pickerSection = fields.querySelector('[data-carddav-addressbook-section]');
		if (pickerSection) pickerSection.classList.remove('hidden');
		var existingInputs = fields.querySelectorAll('input[name="addressbook_url"]');
		var selected = {};
		existingInputs.forEach(function(input) { if (input.value) selected[input.value] = true; });
		var wrap = document.createElement('div');
		wrap.className = pickerSection ? '' : 'rounded-lg border border-border bg-background/60 p-3';
		var title = document.createElement('div');
		title.className = 'text-xs font-semibold text-foreground';
		title.textContent = 'Choose address books';
		wrap.appendChild(title);
		var hint = document.createElement('p');
		hint.className = 'mt-1 text-xs leading-relaxed text-muted-foreground';
		hint.textContent = 'Select one or more address books to sync. The default is used for new contacts.';
		wrap.appendChild(hint);
		var list = document.createElement('div');
		list.className = 'mt-3 space-y-2';
		books.forEach(function(book) {
			var row = document.createElement('label');
			row.className = 'flex w-full items-start gap-3 rounded-md border border-border bg-card px-3 py-2 text-xs transition-colors hover:bg-accent';
			var checkWrap = document.createElement('div');
			checkWrap.className = 'relative mt-0.5 inline-flex items-center';
			var check = document.createElement('input');
			check.type = 'checkbox';
			check.name = 'addressbook_url';
			check.value = book.url || '';
			check.checked = !!selected[book.url] || Object.keys(selected).length === 0;
			check.className = 'peer size-4 shrink-0 rounded-sm border border-input shadow-xs focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-50 checked:bg-primary checked:text-primary-foreground checked:border-primary indeterminate:bg-primary indeterminate:text-primary-foreground indeterminate:border-primary appearance-none cursor-pointer transition-shadow relative';
			var checkIcon = document.createElement('div');
			checkIcon.className = 'absolute inset-0 pointer-events-none flex items-center justify-center text-primary-foreground opacity-0 peer-checked:opacity-100';
			checkIcon.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="size-3.5" data-lucide="icon"><path d="M20 6 9 17l-5-5"></path></svg>';
			checkWrap.appendChild(check);
			checkWrap.appendChild(checkIcon);
			var hiddenName = document.createElement('input');
			hiddenName.type = 'hidden';
			hiddenName.name = 'addressbook_name';
			hiddenName.value = book.name || '';
			hiddenName.disabled = !check.checked;
			var hiddenID = document.createElement('input');
			hiddenID.type = 'hidden';
			hiddenID.name = 'addressbook_id';
			hiddenID.value = book.id || '';
			hiddenID.disabled = !check.checked;
			var content = document.createElement('div');
			content.className = 'min-w-0 flex-1';
			var name = document.createElement('div');
			name.className = 'font-semibold text-foreground';
			name.textContent = book.name || 'Address book';
			var url = document.createElement('div');
			url.className = 'mt-0.5 truncate text-muted-foreground';
			url.textContent = book.url || '';
			var defaultWrap = document.createElement('label');
			defaultWrap.className = 'mt-2 inline-flex items-center gap-1.5 text-xs text-muted-foreground';
			var radio = document.createElement('input');
			radio.type = 'radio';
			radio.name = 'default_addressbook_url';
			radio.value = book.url || '';
			radio.disabled = !check.checked;
			radio.className = 'relative h-4 w-4 before:absolute before:left-1/2 before:top-1/2 before:h-1.5 before:w-1.5 before:-translate-x-1/2 before:-translate-y-1/2 appearance-none rounded-full border-2 border-primary before:content-[\'\'] before:rounded-full before:bg-background checked:border-primary checked:bg-primary checked:before:visible focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed';
			defaultWrap.appendChild(radio);
			defaultWrap.appendChild(document.createTextNode('Default for new contacts'));
			content.appendChild(name);
			content.appendChild(url);
			content.appendChild(defaultWrap);
			row.appendChild(checkWrap);
			row.appendChild(hiddenID);
			row.appendChild(hiddenName);
			row.appendChild(content);
			check.addEventListener('change', function() {
				radio.disabled = !check.checked;
				hiddenName.disabled = !check.checked;
				hiddenID.disabled = !check.checked;
				row.classList.toggle('border-primary', check.checked);
				row.classList.toggle('bg-primary/10', check.checked);
				if (check.checked && !list.querySelector('input[name="default_addressbook_url"]:checked')) radio.checked = true;
				if (!check.checked && radio.checked) {
					radio.checked = false;
					var next = list.querySelector('input[name="addressbook_url"]:checked');
					if (next) next.closest('label').querySelector('input[name="default_addressbook_url"]').checked = true;
				}
			});
			if (check.checked) row.classList.add('border-primary', 'bg-primary/10');
			list.appendChild(row);
		});
		var firstChecked = list.querySelector('input[name="addressbook_url"]:checked');
		if (firstChecked) firstChecked.closest('label').querySelector('input[name="default_addressbook_url"]').checked = true;
		wrap.appendChild(list);
		if (pickerSection) {
			pickerSection.textContent = '';
			pickerSection.appendChild(wrap);
		} else {
			status.appendChild(wrap);
		}
	}
}
document.addEventListener('click', function(event) {
	var button = event.target && event.target.closest ? event.target.closest('[data-carddav-discover]') : null;
	if (!button) return;
	event.preventDefault();
	discoverCardDAV(button.dataset.accountId || '', button.dataset.fieldsId || '', button.dataset.statusId || '', button.dataset.carddavAutodiscover === 'true');
});
document.addEventListener('click', function(event) {
	var button = event.target && event.target.closest ? event.target.closest('[data-carddav-manual]') : null;
	if (!button) return;
	event.preventDefault();
	var fields = document.getElementById(button.dataset.fieldsId || '');
	if (!fields) return;
	var manual = fields.querySelector('[data-carddav-manual-addressbook]');
	if (!manual) return;
	manual.classList.remove('hidden');
	var input = manual.querySelector('input[name="addressbook_url"]');
	if (input) input.focus();
});
document.addEventListener('input', function(event) {
	if (event.target && event.target.closest && event.target.closest('#account-form')) syncAddContactStep();
	var form = event.target && event.target.closest ? event.target.closest('[data-contact-sync-form]') : null;
	if (form) contactSyncResetSave(form.dataset.accountId || '');
});
document.addEventListener('change', function(event) {
	var form = event.target && event.target.closest ? event.target.closest('[data-contact-sync-form]') : null;
	if (form) contactSyncResetSave(form.dataset.accountId || '');
});
document.body.addEventListener('htmx:beforeRequest', function(event) {
	var form = event.target && event.target.closest ? event.target.closest('[data-contact-sync-form]') : null;
	if (form) contactSyncSaving(form.dataset.accountId || '');
});
document.body.addEventListener('htmx:afterRequest', function(event) {
	var form = event.target && event.target.closest ? event.target.closest('[data-contact-sync-form]') : null;
	if (!form) return;
	var xhr = event.detail && event.detail.xhr ? event.detail.xhr : null;
	var ok = !!(event.detail && event.detail.successful) && (!xhr || xhr.getResponseHeader('X-Raven-Status') !== 'error');
	contactSyncSaved(form.dataset.accountId || '', ok);
});
	document.body.addEventListener('htmx:afterRequest', function(event) {
		var target = event.detail && event.detail.elt ? event.detail.elt : event.target;
		var form = target && target.closest ? target.closest('[data-wizard-contact-form]') : null;
	if (!form) return;
		var xhr = event.detail && event.detail.xhr ? event.detail.xhr : null;
		var ok = !!(event.detail && event.detail.successful) && (!xhr || xhr.getResponseHeader('X-Raven-Status') !== 'error');
		if (!ok) return;
		var root = form.closest('#add-account-dialog,#edit-account-dialog');
		var toggle = root && root.querySelector('[data-wizard-service-switch="contacts"]');
		var savedEnabled = xhr ? xhr.getResponseHeader('X-Raven-Contact-Sync-Enabled') : '';
		if (toggle && (savedEnabled === 'true' || savedEnabled === 'false')) {
			toggle.checked = savedEnabled === 'true';
			toggle.dataset.wizardServiceInitialEnabled = savedEnabled;
			syncWizardServiceContent(root);
			if (root.id === 'add-account-dialog') syncAddContactStep();
		}
		if (root) wizardServiceAdvance(root, Number(form.dataset.wizardNextStep || 0));
	});
function refreshCardDAVBaseURLActions(root) {
	(root || document).querySelectorAll('[data-carddav-base-url-action]').forEach(function(group) {
		var fields = document.getElementById(group.dataset.fieldsId || '');
		var base = fields && fields.querySelector ? fields.querySelector('input[name="base_url"]') : null;
		var enabled = !!(base && base.value && base.value.trim());
		var enabledAction = group.querySelector('[data-carddav-enabled-action]');
		var disabledAction = group.querySelector('[data-carddav-disabled-action]');
		if (enabledAction) enabledAction.classList.toggle('!hidden', !enabled);
		if (disabledAction) disabledAction.classList.toggle('!hidden', enabled);
	});
}
document.addEventListener('input', function(event) {
	if (!event.target || !event.target.matches || !event.target.matches('input[name="base_url"]')) return;
	refreshCardDAVBaseURLActions(document);
});
document.addEventListener('DOMContentLoaded', function() { refreshCardDAVBaseURLActions(document); });
document.body.addEventListener('htmx:afterSwap', function(event) {
	refreshCardDAVBaseURLActions(event.target || document);
	var addRoot = document.getElementById('add-account-dialog');
	if (addRoot) syncAddContactStep();
	if (addRoot && typeof _wizStep !== 'undefined') setWizardFooter(addRoot, _wizStep, true);
	if (addRoot) syncWizardServiceContent(addRoot);
	var editRoot = document.getElementById('edit-account-dialog');
	if (editRoot && typeof _editWizStep !== 'undefined') setWizardFooter(editRoot, _editWizStep, true);
	if (editRoot) syncWizardServiceContent(editRoot);
});
document.body.addEventListener('htmx:oobAfterSwap', function() {
	var addRoot = document.getElementById('add-account-dialog');
	if (addRoot) syncAddContactStep();
	if (addRoot && typeof _wizStep !== 'undefined') setWizardFooter(addRoot, _wizStep, true);
	if (addRoot) syncWizardServiceContent(addRoot);
	var editRoot = document.getElementById('edit-account-dialog');
	if (editRoot && typeof _editWizStep !== 'undefined') setWizardFooter(editRoot, _editWizStep, true);
	if (editRoot) syncWizardServiceContent(editRoot);
});
document.body.addEventListener('htmx:afterSettle', function() {
	var addRoot = document.getElementById('add-account-dialog');
	if (addRoot) syncAddContactStep();
	if (addRoot && typeof _wizStep !== 'undefined') setWizardFooter(addRoot, _wizStep, true);
	if (addRoot) syncWizardServiceContent(addRoot);
	var editRoot = document.getElementById('edit-account-dialog');
	if (editRoot && typeof _editWizStep !== 'undefined') setWizardFooter(editRoot, _editWizStep, true);
	if (editRoot) syncWizardServiceContent(editRoot);
});
// The dialog markup is fetched on first open, so the observer that restarts the
// wizard whenever the dialog opens is bound then (and once at load, in case the
// markup is already present).
function bindAddWizardObserver() {
	var dlg = document.querySelector('#add-account-dialog dialog[data-tui-dialog-content]');
	if (!dlg || dlg.dataset.wizardObserverBound === 'true') return;
	dlg.dataset.wizardObserverBound = 'true';
	new MutationObserver(function() {
		var track = document.getElementById('wizard-track');
		if (!track) return;
		if (dlg.open) {
			wizardGo(0, true);
			syncAddContactStep();
			syncWizardServiceContent(document.getElementById('add-account-dialog'));
		}
	}).observe(dlg, { attributes: true, attributeFilter: ['open'] });
}
document.addEventListener('DOMContentLoaded', bindAddWizardObserver);

// Fetches the wizard markup the first time it is needed.
function loadAddAccountDialog() {
	if (document.getElementById('add-account-dialog')) return Promise.resolve();
	return htmx.ajax('GET', '/ui/add-account-dialog', { target: '#add-account-dialog-slot', swap: 'innerHTML' });
}

// Opens the add-account wizard, fetching its markup the first time.
function openAddAccountDialog() {
	return loadAddAccountDialog().then(function() {
		bindAddWizardObserver();
		if (window.tui && window.tui.dialog) window.tui.dialog.open('add-account-dialog');
	});
}
