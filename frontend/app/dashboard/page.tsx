"use client";
import { useState } from "react";

export default function DashboardHome() {
    const [uploading, setUploading] = useState(false);

    const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        const userId = localStorage.getItem("user_id")
        setUploading(true);
        const formData = new FormData();
        formData.append("file", file);

        try {
            // Hardcoding user_id 1 for now; we'll fix this with the JWT later
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const response = await fetch(`${apiUrl}/upload/${userId}`, {
                method: "POST",
                body: formData,
            });

            if (response.ok) {
                alert("Upload successful! Parsing in background...");
            }
        } catch (error) {
            alert("Upload failed. Is the backend running?");
        } finally {
            setUploading(false);
        }
    };

    return (
        <div className="flex flex-col items-center justify-center min-h-[60vh] text-black">
            <h1 className="text-5xl font-bold mb-10">Welcome!</h1>

            <label className="cursor-pointer">
                <div className="bg-blue-100 hover:bg-blue-200 text-blue-700 px-10 py-6 rounded-full shadow-md transition-all font-semibold text-center leading-tight">
                    {uploading ? "Uploading..." : "Upload Syllabus"}
                </div>
                <input type="file" className="hidden" accept=".pdf" onChange={handleFileChange} />
            </label>
        </div>
    );
}