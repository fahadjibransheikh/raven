// Raven's macOS agenda widget. It reads the snapshot the Raven server writes
// into this extension's sandbox container (RAVEN_WIDGET_SNAPSHOT_PATH, see
// internal/handler/calendar_widget.go) and never talks to the server itself.
import SwiftUI
import WidgetKit

struct AgendaSnapshot: Decodable {
    var generatedAt: String
    var timezone: String
    var events: [AgendaEvent]
}

struct AgendaEvent: Decodable, Identifiable {
    var id: String
    var summary: String
    var location: String?
    var color: String?
    var responseStatus: String?
    var allDay: Bool
    var start: String?
    var end: String?
    var startDate: String?
    var endDate: String?
}

/// One row as shown on a given day.
struct AgendaItem: Identifiable {
    var id: String
    var title: String
    var location: String
    var color: Color
    var allDay: Bool
    var start: Date
    var end: Date
    var declined: Bool
}

struct AgendaDay: Identifiable {
    var id: Date { date }
    var date: Date
    var items: [AgendaItem]
}

struct AgendaEntry: TimelineEntry {
    var date: Date
    var days: [AgendaDay]
    var calendar: Calendar
    var updatedAt: Date?
    var missing: Bool
}

enum AgendaStore {
    static var fileURL: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Raven/agenda.json")
    }

    static func load() -> AgendaSnapshot? {
        guard let data = try? Data(contentsOf: fileURL) else { return nil }
        return try? JSONDecoder().decode(AgendaSnapshot.self, from: data)
    }

    static let instant = ISO8601DateFormatter()

    static func days(_ snapshot: AgendaSnapshot?, calendar: Calendar, now: Date) -> [AgendaDay] {
        let today = calendar.startOfDay(for: now)
        let dates = (0..<3).compactMap { calendar.date(byAdding: .day, value: $0, to: today) }
        let dateKey = DateFormatter()
        dateKey.locale = Locale(identifier: "en_US_POSIX")
        dateKey.calendar = calendar
        dateKey.timeZone = calendar.timeZone
        dateKey.dateFormat = "yyyy-MM-dd"
        let items: [AgendaItem] = (snapshot?.events ?? []).compactMap { event in
            let start: Date?
            let end: Date?
            if event.allDay {
                start = event.startDate.flatMap(dateKey.date(from:))
                end = event.endDate.flatMap(dateKey.date(from:))
            } else {
                start = event.start.flatMap(instant.date(from:))
                end = event.end.flatMap(instant.date(from:))
            }
            guard let start, let end else { return nil }
            return AgendaItem(
                id: event.id, title: event.summary.isEmpty ? "(No title)" : event.summary,
                location: event.location ?? "", color: Color(hex: event.color), allDay: event.allDay,
                start: start, end: end, declined: event.responseStatus == "declined")
        }
        return dates.map { day in
            let next = calendar.date(byAdding: .day, value: 1, to: day) ?? day
            let dayItems = items.filter { item in
                // Today hides timed events that already ended; all-day stays.
                guard item.start < next, item.end > day || item.start == item.end && item.start >= day else { return false }
                return item.allDay || day != today || item.end > now
            }
            return AgendaDay(date: day, items: dayItems)
        }
    }
}

struct Provider: TimelineProvider {
    func placeholder(in context: Context) -> AgendaEntry {
        AgendaEntry(date: .now, days: [], calendar: .current, updatedAt: nil, missing: false)
    }

    func getSnapshot(in context: Context, completion: @escaping (AgendaEntry) -> Void) {
        completion(entry(at: .now, snapshot: AgendaStore.load()))
    }

    // Entries flip at every event start/end so "Now" and "Next" stay right
    // between reloads; the reload picks up a newer snapshot from Raven.
    func getTimeline(in context: Context, completion: @escaping (Timeline<AgendaEntry>) -> Void) {
        let snapshot = AgendaStore.load()
        let now = Date.now
        let horizon = now.addingTimeInterval(6 * 3600)
        var moments = Set([now])
        for event in snapshot?.events ?? [] where !event.allDay {
            for value in [event.start, event.end].compactMap({ $0.flatMap(AgendaStore.instant.date(from:)) })
            where value > now && value < horizon {
                moments.insert(value)
            }
        }
        if let midnight = calendar(snapshot).nextDate(after: now, matching: DateComponents(hour: 0), matchingPolicy: .nextTime),
           midnight < horizon {
            moments.insert(midnight)
        }
        let entries = moments.sorted().map { entry(at: $0, snapshot: snapshot) }
        completion(Timeline(entries: entries, policy: .after(now.addingTimeInterval(30 * 60))))
    }

    private func calendar(_ snapshot: AgendaSnapshot?) -> Calendar {
        var calendar = Calendar.current
        if let name = snapshot?.timezone, !name.isEmpty, let zone = TimeZone(identifier: name) {
            calendar.timeZone = zone
        }
        return calendar
    }

    private func entry(at date: Date, snapshot: AgendaSnapshot?) -> AgendaEntry {
        let calendar = calendar(snapshot)
        return AgendaEntry(
            date: date, days: AgendaStore.days(snapshot, calendar: calendar, now: date), calendar: calendar,
            updatedAt: snapshot.flatMap { AgendaStore.instant.date(from: $0.generatedAt) }, missing: snapshot == nil)
    }
}

struct AgendaView: View {
    @Environment(\.widgetFamily) private var family
    var entry: AgendaEntry

    // Fixed row heights so the row count can be worked out from the widget's
    // height; widget text doesn't scale with Dynamic Type on macOS.
    private static let spacing: CGFloat = 6
    private static let headerHeight: CGFloat = 16
    private static let dayHeight: CGFloat = 16
    private static let moreHeight: CGFloat = 14
    static func eventHeight(compact: Bool) -> CGFloat { compact ? 16 : 28 }

    var body: some View {
        GeometryReader { geometry in
            VStack(alignment: .leading, spacing: Self.spacing) {
                header.frame(height: Self.headerHeight)
                if entry.missing {
                    Spacer()
                    Text("Open Raven to load your calendar.")
                        .font(.caption).foregroundStyle(.secondary)
                    Spacer()
                } else {
                    rows(height: geometry.size.height)
                    Spacer(minLength: 0)
                }
            }
        }
        .containerBackground(.background, for: .widget)
        .widgetURL(URL(string: "raven://calendar"))
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(entry.date, format: Date.FormatStyle(timeZone: entry.calendar.timeZone).weekday(.wide))
                .font(.caption.weight(.semibold)).foregroundStyle(.red)
                .textCase(.uppercase)
            Text(entry.date, format: Date.FormatStyle(timeZone: entry.calendar.timeZone).month(.abbreviated).day())
                .font(.caption.weight(.semibold))
            Spacer()
            if family != .systemSmall, let updated = entry.updatedAt, entry.date.timeIntervalSince(updated) > 3600 {
                Text("Updated \(updated, style: .relative) ago").font(.caption2).foregroundStyle(.tertiary)
            }
        }
        .lineLimit(1)
    }

    // How many rows fit in `height` below the header, keeping room for the
    // "+N more" line when rows are left over and never ending on a day heading.
    static func fittingCount(_ rows: [Row], height: CGFloat, compact: Bool) -> Int {
        var used = headerHeight
        var count = 0
        for (index, row) in rows.enumerated() {
            let rowHeight: CGFloat
            if case .day = row.kind { rowHeight = dayHeight } else { rowHeight = eventHeight(compact: compact) }
            let more = index + 1 < rows.count ? spacing + moreHeight : 0
            if used + spacing + rowHeight + more > height { break }
            used += spacing + rowHeight
            count += 1
        }
        while count > 0, case .day = rows[count - 1].kind { count -= 1 }
        return count
    }

    @ViewBuilder private func rows(height: CGFloat) -> some View {
        let compact = family == .systemSmall
        let shown = Array(visibleRows.prefix(Self.fittingCount(visibleRows, height: height, compact: compact)))
        if shown.isEmpty {
            Text("Nothing scheduled in the next 3 days")
                .font(.caption).foregroundStyle(.secondary)
        }
        ForEach(shown) { row in
            switch row.kind {
            case .day(let date):
                Text(dayLabel(date)).font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                    .lineLimit(1).frame(height: Self.dayHeight, alignment: .bottom)
            case .event(let item):
                EventRow(item: item, now: entry.date, calendar: entry.calendar, compact: compact)
            }
        }
        let hidden = visibleRows.dropFirst(shown.count).filter { if case .event = $0.kind { return true } else { return false } }.count
        if hidden > 0 {
            Text("+\(hidden) more").font(.caption2).foregroundStyle(.secondary).frame(height: Self.moreHeight)
        }
    }

    struct Row: Identifiable {
        enum Kind { case day(Date), event(AgendaItem) }
        var id: String
        var kind: Kind
    }

    // Today's events come first without a heading; later days get one.
    private var visibleRows: [Row] {
        var rows: [Row] = []
        for (index, day) in entry.days.enumerated() where !day.items.isEmpty || index == 0 {
            if index > 0 { rows.append(Row(id: "day-\(day.date.timeIntervalSince1970)", kind: .day(day.date))) }
            rows += day.items.map { Row(id: "\(day.date.timeIntervalSince1970)-\($0.id)", kind: .event($0)) }
        }
        return rows
    }

    private func dayLabel(_ date: Date) -> String {
        if entry.calendar.isDateInTomorrow(date) { return "Tomorrow" }
        let formatter = DateFormatter()
        formatter.calendar = entry.calendar
        formatter.timeZone = entry.calendar.timeZone
        formatter.setLocalizedDateFormatFromTemplate("EEEEMMMd")
        return formatter.string(from: date)
    }
}

struct EventRow: View {
    var item: AgendaItem
    var now: Date
    var calendar: Calendar
    var compact: Bool

    var body: some View {
        let happening = !item.allDay && item.start <= now && item.end > now
        HStack(spacing: 6) {
            RoundedRectangle(cornerRadius: 1.5).fill(item.color).frame(width: 3)
            VStack(alignment: .leading, spacing: 0) {
                Text(item.title).font(.caption.weight(.medium)).lineLimit(1)
                    .strikethrough(item.declined)
                if !compact {
                    Text(detail(happening: happening)).font(.caption2).foregroundStyle(happening ? .red : .secondary).lineLimit(1)
                }
            }
        }
        .frame(height: AgendaView.eventHeight(compact: compact), alignment: .leading)
        .opacity(item.declined ? 0.5 : 1)
    }

    private func detail(happening: Bool) -> String {
        let time: String
        if item.allDay {
            time = "All day"
        } else if happening {
            time = "Now · until " + clock(item.end)
        } else {
            time = clock(item.start) + "–" + clock(item.end)
        }
        return item.location.isEmpty ? time : time + " · " + item.location
    }

    private func clock(_ date: Date) -> String {
        date.formatted(Date.FormatStyle(timeZone: calendar.timeZone).hour().minute())
    }
}

extension Color {
    /// #rrggbb from the snapshot; Raven's default event color otherwise.
    init(hex: String?) {
        guard let hex, hex.count == 7, let value = UInt32(hex.dropFirst(), radix: 16) else {
            self = Color(red: 0.93, green: 0.36, blue: 0.6)
            return
        }
        self = Color(
            red: Double((value >> 16) & 0xff) / 255, green: Double((value >> 8) & 0xff) / 255,
            blue: Double(value & 0xff) / 255)
    }
}

@main
struct RavenAgendaWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "RavenAgenda", provider: Provider()) { AgendaView(entry: $0) }
            .configurationDisplayName("Raven Agenda")
            .description("Your next 3 days from Raven Calendar.")
            .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
