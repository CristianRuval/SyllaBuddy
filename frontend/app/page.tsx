"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { GoogleLogin } from "@react-oauth/google";

export default function LoginPage() {
    const router = useRouter();

    // Form State
    const [email, setEmail] = useState("");
    const [username, setUsername] = useState("");
    const [password, setPassword] = useState("");

    // NEW: Beta Invite Code State
    const [inviteCode, setInviteCode] = useState("");

    const [isRegistering, setIsRegistering] = useState(false);
    const [error, setError] = useState("");

    // --- 1. Handle Standard Login/Register ---
    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");

        const endpoint = isRegistering ? "register" : "login";

        // UPDATED: Include invite_code in the payload
        const payload = isRegistering
            ? { username, email, password, invite_code: inviteCode }
            : { email, password };

        try {
            // NOTE: Use NEXT_PUBLIC_API_URL or fallback to localhost for Docker compatibility
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";

            const response = await fetch(`${apiUrl}/${endpoint}`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(payload),
            });

            if (response.ok) {
                if (isRegistering) {
                    alert("Registration successful! Please sign in.");
                    setIsRegistering(false);
                    setPassword("");
                    setInviteCode(""); // Clear the code
                } else {
                    const data = await response.json();
                    localStorage.setItem("user_id", data.user_id);
                    localStorage.setItem("token", data.access_token);
                    router.push("/dashboard");
                }
            } else {
                const errData = await response.json();
                setError(errData.detail || "Action failed");
            }
        } catch (err) {
            setError("Backend server is offline.");
        }
    };

    // --- 2. Handle Google Login Success ---
    const handleGoogleSuccess = async (credentialResponse: any) => {
        setError("");
        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/auth/google`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ token: credentialResponse.credential }),
            });

            const data = await res.json();

            if (res.ok) {
                localStorage.setItem("token", data.access_token);
                localStorage.setItem("user_id", data.user_id);
                router.push("/dashboard");
            } else {
                setError(data.detail || "Google Login failed");
            }
        } catch (err) {
            setError("Server connection failed during Google Login");
        }
    };

    return (
        <main className="flex min-h-screen items-center justify-center bg-gray-50 text-black px-4 font-sans">
            <div className="w-full max-w-md p-10 bg-white rounded-2xl shadow-xl border border-gray-100">
                <h1 className="text-4xl font-extrabold text-blue-600 text-center mb-2">SyllaBuddy</h1>
                <p className="text-gray-500 text-center mb-8">
                    {isRegistering ? "Join the Private Beta" : "Simplify your semester. Start here."}
                </p>

                {/* Standard Form */}
                <form onSubmit={handleSubmit} className="space-y-4">
                    {isRegistering && (
                        <>
                            <input
                                type="text"
                                placeholder="Username"
                                value={username}
                                onChange={(e) => setUsername(e.target.value)}
                                className="w-full p-3 border border-gray-300 rounded-lg outline-none focus:ring-2 focus:ring-blue-500 transition-all"
                                required
                            />
                            {/* NEW: Invite Code Input */}
                            <input
                                type="text"
                                placeholder="Beta Invite Code"
                                value={inviteCode}
                                onChange={(e) => setInviteCode(e.target.value)}
                                className="w-full p-3 border-2 border-yellow-100 bg-yellow-50 rounded-lg outline-none focus:ring-2 focus:ring-yellow-400 focus:border-yellow-400 transition-all placeholder-gray-400 text-gray-800"
                                required
                            />
                        </>
                    )}
                    <input
                        type="email"
                        placeholder="Email address"
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        className="w-full p-3 border border-gray-300 rounded-lg outline-none focus:ring-2 focus:ring-blue-500 transition-all"
                        required
                    />
                    <input
                        type="password"
                        placeholder="Password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        className="w-full p-3 border border-gray-300 rounded-lg outline-none focus:ring-2 focus:ring-blue-500 transition-all"
                        required
                    />
                    <button
                        type="submit"
                        className="w-full py-3 bg-blue-600 text-white font-bold rounded-lg hover:bg-blue-700 transition-all shadow-md active:scale-[0.98]"
                    >
                        {isRegistering ? "Sign Up" : "Sign In"}
                    </button>
                </form>

                {/* Divider */}
                <div className="my-6 flex items-center">
                    <div className="flex-1 border-t border-gray-200"></div>
                    <span className="px-3 text-gray-400 text-xs font-semibold tracking-wider">OR CONTINUE WITH</span>
                    <div className="flex-1 border-t border-gray-200"></div>
                </div>

                {/* Google Button */}
                <div className="flex justify-center">
                    <GoogleLogin
                        onSuccess={handleGoogleSuccess}
                        onError={() => setError("Google Login Failed")}
                        theme="outline"
                        size="large"
                        text="continue_with"
                        shape="pill"
                    />
                </div>

                {/* Error Message Display */}
                {error && (
                    <div className="mt-4 p-3 bg-red-50 border border-red-100 rounded-lg text-center">
                        <p className="text-red-600 text-sm font-medium">{error}</p>
                    </div>
                )}

                {/* Switch between Login/Register */}
                <p className="mt-8 text-center text-sm text-gray-600">
                    {isRegistering ? "Already have an account?" : "Don't have an account?"}{" "}
                    <button
                        onClick={() => {
                            setIsRegistering(!isRegistering);
                            setError("");
                        }}
                        className="text-blue-600 font-bold hover:underline"
                    >
                        {isRegistering ? "Sign In" : "Register here"}
                    </button>
                </p>
            </div>
        </main>
    );
}