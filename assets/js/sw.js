self.addEventListener("push", function (event) {
  var data = {}
  if (event.data) {
    try { data = event.data.json() || {} } catch (_) { data = { body: event.data.text() } }
  }

  var title = data.title || "Raven"
  var icon = data.icon || data.avatar_url || "/assets/logo.png"
  var options = {
    body: data.body || "New notification",
    icon: icon,
    badge: "/assets/logo.png",
    tag: data.tag || "raven-notification",
    data: {
      url: data.url || "/",
      folderID: data.folder_id || "",
      joinURL: /^https?:\/\//i.test(data.join_url || "") ? data.join_url : "",
    },
  }
  // Meeting reminders stay until dismissed and offer Join where the browser shows actions.
  if (data.require_interaction) options.requireInteraction = true
  if (options.data.joinURL) options.actions = [{ action: "join", title: "Join meeting" }]

  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener("notificationclick", function (event) {
  event.notification.close()
  // A meeting reminder opens the meeting itself, from its Join action or the notification body.
  var joinURL = event.notification.data && event.notification.data.joinURL
  if (joinURL) {
    if (clients.openWindow) event.waitUntil(clients.openWindow(joinURL))
    return
  }
  var targetURL = new URL((event.notification.data && event.notification.data.url) || "/", self.location.origin).href

  event.waitUntil(clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (clientList) {
    for (var i = 0; i < clientList.length; i++) {
      var client = clientList[i]
      if (client.url && new URL(client.url).origin === self.location.origin) {
        client.navigate(targetURL)
        return client.focus()
      }
    }
    if (clients.openWindow) return clients.openWindow(targetURL)
  }))
})
