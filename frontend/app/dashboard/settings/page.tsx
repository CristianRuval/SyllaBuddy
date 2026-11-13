"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

export default function SettingsPage() {
    const router = useRouter();
    const [user, setUser] = useState<{ username: string; id: number; email?: string } | null>(null);
    const [loading, setLoading] = useState(true);

    // Form States
    const [newUsername, setNewUsername] = useState("");
    const [currentPassword, setCurrentPassword] = useState("");
    const [newPassword, setNewPassword] = useState("");

    // Feedback States
    const [msg, setMsg] = useState<{ text: string; type: 'success' | 'error' } | null>(null);

    useEffect(() => {
        const token = localStorage.getItem("token") || localStorage.getItem("access_token");
        if (!token) {
            router.push("/");
            return;
        }

        // Decode token to get basic info
        try {
            const payload = JSON.parse(atob(token.split('.')[1]));
            // We set the initial state from the token, but you might want to fetch fresh data from DB here
            setUser({ username: payload.sub, id: payload.user_id });
            setNewUsername(payload.sub);
        } catch (e) {
            console.error(e);
        } finally {
            setLoading(false);
        }
    }, [router]);

    const handleUpdateProfile = async (e: React.FormEvent) => {
        e.preventDefault();
        setMsg(null);
        const token = localStorage.getItem("token") || localStorage.getItem("access_token");

        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/users/me/profile`, {
                method: "PUT",
                headers: {
                    "Content-Type": "application/json",
                    "Authorization": `Bearer ${token}`
                },
                body: JSON.stringify({ username: newUsername })
            });

            const data = await res.json();

            if (res.ok) {
                setMsg({ text: "Username updated! Please log in again to see changes.", type: "success" });
                // Optional: Update local user state immediately
                setUser(prev => prev ? { ...prev, username: newUsername } : null);
            } else {
                setMsg({ text: data.detail || "Update failed", type: "error" });
            }
        } catch (err) {
            setMsg({ text: "Server error", type: "error" });
        }
    };

    const handleChangePassword = async (e: React.FormEvent) => {
        e.preventDefault();
        setMsg(null);
        const token = localStorage.getItem("token") || localStorage.getItem("access_token");

        if (newPassword.length < 6) {
            setMsg({ text: "New password must be at least 6 characters", type: "error" });
            return;
        }

        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/users/me/password`, {
                method: "PUT",
                headers: {
                    "Content-Type": "application/json",
                    "Authorization": `Bearer ${token}`
                },
                body: JSON.stringify({ current_password: currentPassword, new_password: newPassword })
            });

            const data = await res.json();

            if (res.ok) {
                setMsg({ text: "Password changed successfully.", type: "success" });
                setCurrentPassword("");
                setNewPassword("");
            } else {
                setMsg({ text: data.detail || "Failed to change password", type: "error" });
            }
        } catch (err) {
            setMsg({ text: "Server error", type: "error" });
        }
    };

    const handleLogout = () => {
        localStorage.clear();
        router.push("/");
    };

    if (loading) return <div className="p-10">Loading...</div>;

    return (
        <div className="max-w-3xl mx-auto p-8 font-sans">
            <h1 className="text-3xl font-bold text-gray-900 mb-8">Account Settings</h1>

            {/* Notification Banner */}
            {msg && (
                <div className={`mb-6 p-4 rounded-lg text-sm font-medium ${msg.type === 'success' ? 'bg-green-50 text-green-700 border border-green-200' : 'bg-red-50 text-red-700 border border-red-200'
                    }`}>
                    {msg.text}
                </div>
            )}

            {/* --- SECTION 1: PROFILE --- */}
            <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden mb-8">
                <div className="p-6 border-b border-gray-100 bg-gray-50">
                    <h2 className="text-lg font-bold text-gray-800">General Information</h2>
                    <p className="text-xs text-gray-500 mt-1">Update your public display name.</p>
                </div>
                <div className="p-6">
                    <form onSubmit={handleUpdateProfile} className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">Username</label>
                            <input
                                type="text"
                                value={newUsername}
                                onChange={(e) => setNewUsername(e.target.value)}
                                className="w-full p-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none transition-all"
                            />
                        </div>
                        <div className="flex justify-end">
                            <button
                                type="submit"
                                className="bg-blue-600 hover:bg-blue-700 text-white font-semibold py-2 px-6 rounded-lg transition-colors text-sm"
                            >
                                Save Changes
                            </button>
                        </div>
                    </form>
                </div>
            </div>

            {/* --- SECTION 2: SECURITY --- */}
            <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden mb-8">
                <div className="p-6 border-b border-gray-100 bg-gray-50">
                    <h2 className="text-lg font-bold text-gray-800">Security</h2>
                    <p className="text-xs text-gray-500 mt-1">Change your password.</p>
                </div>
                <div className="p-6">
                    <form onSubmit={handleChangePassword} className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">Current Password</label>
                            <input
                                type="password"
                                value={currentPassword}
                                onChange={(e) => setCurrentPassword(e.target.value)}
                                className="w-full p-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                            />
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">New Password</label>
                            <input
                                type="password"
                                value={newPassword}
                                onChange={(e) => setNewPassword(e.target.value)}
                                className="w-full p-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                            />
                        </div>
                        <div className="flex justify-end">
                            <button
                                type="submit"
                                disabled={!currentPassword || !newPassword}
                                className={`font-semibold py-2 px-6 rounded-lg transition-colors text-sm ${!currentPassword || !newPassword
                                        ? 'bg-gray-100 text-gray-400 cursor-not-allowed'
                                        : 'bg-gray-800 hover:bg-gray-900 text-white'
                                    }`}
                            >
                                Update Password
                            </button>
                        </div>
                    </form>
                </div>
            </div>

            {/* --- LOGOUT AREA --- */}
            <div className="flex justify-end">
                <button
                    onClick={handleLogout}
                    className="text-red-600 hover:text-red-800 text-sm font-semibold hover:underline"
                >
                    Sign out of account
                </button>
            </div>
        </div>
    );
}