"use client";
import { useEffect, useState } from "react";
import { useGoogleLogin } from "@react-oauth/google";
import Link from "next/link";

interface CalendarEvent {
    title: string;
    date: string;
    type: string;
    description?: string;
    course_name?: string;
    syllabus_id?: number; // We added this in the backend
}

interface GroupedEvents {
    [key: number]: {
        courseName: string;
        events: CalendarEvent[];
    };
}

export default function MasterCalendarPage() {
    const [groupedEvents, setGroupedEvents] = useState<GroupedEvents>({});
    const [loading, setLoading] = useState(true);
    const [isEmpty, setIsEmpty] = useState(false);

    // Editing State
    const [editingEvent, setEditingEvent] = useState<{ syllabusId: number, title: string } | null>(null);
    const [newDate, setNewDate] = useState("");

    const handleEditClick = (event: CalendarEvent, syllabusId: number) => {
        setEditingEvent({ syllabusId, title: event.title });
        setNewDate(event.date);
    };

    const handleCancelEdit = () => {
        setEditingEvent(null);
        setNewDate("");
    };

    const handleSaveDate = async () => {
        if (!editingEvent) return;

        const token = localStorage.getItem("token") || localStorage.getItem("access_token");
        if (!token) return;

        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/syllabi/${editingEvent.syllabusId}/events`, {
                method: "PUT",
                headers: {
                    "Content-Type": "application/json",
                    "Authorization": `Bearer ${token}`
                },
                body: JSON.stringify({
                    title: editingEvent.title,
                    new_date: newDate
                })
            });

            if (res.ok) {
                // Update local state
                setGroupedEvents(prev => {
                    const newGrouped = { ...prev };
                    const group = newGrouped[editingEvent.syllabusId];
                    if (group) {
                        group.events = group.events.map(ev =>
                            ev.title === editingEvent.title ? { ...ev, date: newDate } : ev
                        );
                    }
                    return newGrouped;
                });
                setEditingEvent(null);
            } else {
                alert("Failed to update date");
            }
        } catch (e) {
            console.error(e);
            alert("Error updating date");
        }
    };

    useEffect(() => {
        const fetchAllEvents = async () => {
            const token = localStorage.getItem("token") || localStorage.getItem("access_token");
            if (!token) return;

            try {
                const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
                const res = await fetch(`${apiUrl}/events/all`, {
                    headers: { Authorization: `Bearer ${token}` }
                });

                if (res.ok) {
                    const data = await res.json();
                    const rawEvents: CalendarEvent[] = data.events || [];

                    if (rawEvents.length === 0) {
                        setIsEmpty(true);
                    } else {
                        // Group events by syllabus_id
                        const groups: GroupedEvents = {};
                        rawEvents.forEach(event => {
                            const id = event.syllabus_id || 0;
                            if (!groups[id]) {
                                groups[id] = {
                                    courseName: event.course_name || "Unknown Course",
                                    events: []
                                };
                            }
                            groups[id].events.push(event);
                        });
                        setGroupedEvents(groups);
                    }
                }
            } catch (err) {
                console.error("Failed to load calendar:", err);
            } finally {
                setLoading(false);
            }
        };

        fetchAllEvents();
    }, []);
    const [isSyncing, setIsSyncing] = useState(false);

    const syncToGoogle = useGoogleLogin({
        scope: "https://www.googleapis.com/auth/calendar", // Request permission to edit calendar
        onSuccess: async (tokenResponse) => {
            setIsSyncing(true);
            const token = localStorage.getItem("token"); // SyllaBuddy Token

            try {
                // Send the GOOGLE Access Token to your backend
                const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
                const res = await fetch(`${apiUrl}/events/sync-google`, {
                    method: "POST",
                    headers: {
                        "Content-Type": "application/json",
                        "Authorization": `Bearer ${token}`
                    },
                    body: JSON.stringify({ google_token: tokenResponse.access_token })
                });

                const data = await res.json();
                if (res.ok) {
                    alert(`Successfully added ${data.synced} events to your Google Calendar!`);
                } else {
                    alert("Sync failed: " + (data.detail || "Unknown error"));
                }
            } catch (e) {
                console.error(e);
                alert("Server error during sync.");
            } finally {
                setIsSyncing(false);
            }
        },
        onError: () => alert("Google permission denied.")
    });
    const handleDownloadSingle = async (id: number, courseName: string) => {
        const token = localStorage.getItem("token") || localStorage.getItem("access_token");
        if (!token) return;

        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/download-calendar/${id}`, {
                headers: { Authorization: `Bearer ${token}` }
            });

            if (res.ok) {
                const blob = await res.blob();
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = `${courseName.replace(/\s+/g, '_')}.ics`;
                document.body.appendChild(a);
                a.click();
                a.remove();
            } else {
                alert("Download failed.");
            }
        } catch (e) {
            console.error(e);
        }
    };

    const handleDownloadAll = async () => {
        const token = localStorage.getItem("token") || localStorage.getItem("access_token");
        if (!token) return;
        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/download-calendar/all`, {
                headers: { Authorization: `Bearer ${token}` }
            });
            if (res.ok) {
                const blob = await res.blob();
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = "Semester_Schedule.ics";
                document.body.appendChild(a);
                a.click();
                a.remove();
            }
        } catch (e) { console.error(e); }
    };

    if (loading) return (
        <div className="flex items-center justify-center min-h-[60vh]">
            <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
        </div>
    );

    return (
        <div className="max-w-5xl mx-auto p-8 font-sans">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-end mb-8 pb-6 border-b border-gray-200">
                <div>
                    <h1 className="text-3xl font-bold text-gray-900">Course Calendars</h1>
                    <p className="text-gray-500 mt-1">
                        View events by course or download your master schedule.
                    </p>
                </div>

                {/* --- BUTTONS SECTION --- */}
                <div className="flex gap-3 mt-4 md:mt-0">
                    {/* 1. NEW GOOGLE BUTTON (Moved Here) */}
                    <button
                        onClick={() => syncToGoogle()}
                        disabled={isEmpty || isSyncing}
                        className={`text-gray-700 bg-white border border-gray-300 hover:bg-gray-50 font-bold py-2.5 px-4 rounded-lg shadow-sm transition-all flex items-center gap-2 ${(isEmpty || isSyncing) ? "opacity-50 cursor-not-allowed" : "active:scale-95"
                            }`}
                    >
                        {isSyncing ? (
                            <span className="animate-spin h-5 w-5 border-2 border-gray-500 border-t-transparent rounded-full"></span>
                        ) : (
                            <svg className="w-5 h-5" viewBox="0 0 24 24">
                                <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4" />
                                <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
                                <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
                                <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
                            </svg>
                        )}
                        {isSyncing ? "Syncing..." : "Sync to Google"}
                    </button>

                    {/* 2. EXISTING DOWNLOAD BUTTON */}
                    <button
                        onClick={handleDownloadAll}
                        disabled={isEmpty}
                        className={`text-white font-bold py-2.5 px-6 rounded-lg shadow transition-all flex items-center gap-2 ${isEmpty ? "bg-gray-400 cursor-not-allowed" : "bg-green-600 hover:bg-green-700 active:scale-95"
                            }`}
                    >
                        <span className="text-lg">📥</span> Download .ICS
                    </button>
                </div>
            </div>

            {isEmpty ? (
                <div className="p-16 text-center bg-gray-50 rounded-xl border border-dashed border-gray-300">
                    <p className="text-gray-400 text-lg">No events found.</p>
                    <Link href="/dashboard" className="text-blue-600 font-bold mt-4 inline-block hover:underline">
                        Upload a Syllabus to get started &rarr;
                    </Link>
                </div>
            ) : (
                <div className="space-y-8">
                    {/* Iterate through each Group (Syllabus) */}
                    {Object.entries(groupedEvents).map(([idStr, group]) => {
                        const id = parseInt(idStr);
                        return (
                            <div key={id} className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
                                {/* Card Header */}
                                <div className="bg-gray-50 px-6 py-4 border-b border-gray-200 flex justify-between items-center">
                                    <h2 className="text-lg font-bold text-gray-800 flex items-center gap-2">
                                        <span className="w-2 h-6 bg-blue-600 rounded-full"></span>
                                        {group.courseName}
                                    </h2>

                                    {/* Small Download Icon (Kept inside card for individual download) */}
                                    <button
                                        onClick={() => handleDownloadSingle(id, group.courseName)}
                                        className="text-gray-500 hover:text-blue-600 hover:bg-blue-50 p-2 rounded-full transition-colors flex items-center gap-2 text-sm font-medium"
                                        title="Download .ICS for this course only"
                                    >
                                        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" className="w-5 h-5">
                                            <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M12 9.75v10.5m0 0 3-3m-3 3-3-3m-7.5-6L9 5.25 15.75 12" />
                                        </svg>
                                    </button>
                                </div>

                                {/* Events Table */}
                                <table className="min-w-full divide-y divide-gray-100">
                                    <thead className="bg-white">
                                        <tr>
                                            <th className="px-6 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider w-32">Date</th>
                                            <th className="px-6 py-3 text-left text-xs font-semibold text-gray-400 uppercase tracking-wider">Event</th>
                                            <th className="px-6 py-3 text-right text-xs font-semibold text-gray-400 uppercase tracking-wider w-32">Type</th>
                                        </tr>
                                    </thead>
                                    <tbody className="divide-y divide-gray-50">
                                        {group.events.map((event: CalendarEvent, idx: number) => {
                                            const isEditing = editingEvent?.syllabusId === id && editingEvent?.title === event.title;
                                            return (
                                                <tr key={idx} className="hover:bg-blue-50/30 transition-colors">
                                                    <td className="px-6 py-3 whitespace-nowrap text-sm font-medium text-gray-900">
                                                        {isEditing ? (
                                                            <div className="flex items-center gap-2">
                                                                <input
                                                                    type="date"
                                                                    value={newDate}
                                                                    onChange={(e) => setNewDate(e.target.value)}
                                                                    className="border border-gray-300 rounded px-2 py-1 text-sm focus:outline-none focus:border-blue-500"
                                                                />
                                                                <button onClick={handleSaveDate} className="text-green-600 hover:text-green-800">✓</button>
                                                                <button onClick={handleCancelEdit} className="text-red-600 hover:text-red-800">✕</button>
                                                            </div>
                                                        ) : (
                                                            <div className="flex items-center gap-2 group">
                                                                {event.date}
                                                                <button
                                                                    onClick={() => handleEditClick(event, id)}
                                                                    className="opacity-0 group-hover:opacity-100 text-gray-400 hover:text-blue-600 transition-opacity"
                                                                    title="Edit Date"
                                                                >
                                                                    ✎
                                                                </button>
                                                            </div>
                                                        )}
                                                    </td>
                                                    <td className="px-6 py-3 text-sm text-gray-700">
                                                        <div className="font-semibold">{event.title}</div>
                                                        {event.description && <div className="text-xs text-gray-400 mt-0.5">{event.description}</div>}
                                                    </td>
                                                    <td className="px-6 py-3 whitespace-nowrap text-right">
                                                        <span className={`px-2.5 py-0.5 rounded-full text-[10px] font-bold uppercase tracking-wide ${event.type?.toLowerCase().includes('exam')
                                                            ? 'bg-red-50 text-red-600 border border-red-100'
                                                            : 'bg-gray-100 text-gray-600 border border-gray-200'
                                                            }`}>
                                                            {event.type || 'Event'}
                                                        </span>
                                                    </td>
                                                </tr>
                                            );
                                        })}
                                    </tbody>
                                </table>
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}