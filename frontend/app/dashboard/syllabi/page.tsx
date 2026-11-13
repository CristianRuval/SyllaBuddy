"use client";
import { useEffect, useState, useRef } from "react";
import Link from "next/link";

// --- Types ---
interface Syllabus {
    id: number;
    course_name: string;
    file_path: string;
    upload_date: string;
    parser_output: any;
}

// --- Helper Component for Secure Images ---
const PDFThumbnail = ({ id, token }: { id: number; token: string }) => {
    const [imgUrl, setImgUrl] = useState<string | null>(null);
    const [error, setError] = useState(false);

    useEffect(() => {
        if (!id || !token) return;

        let active = true;
        const fetchThumbnail = async () => {
            try {
                const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
                const res = await fetch(`${apiUrl}/thumbnail/${id}`, {
                    headers: { Authorization: `Bearer ${token}` },
                });

                if (res.ok) {
                    const blob = await res.blob();
                    const url = URL.createObjectURL(blob);
                    if (active) {
                        setImgUrl(url);
                        setError(false);
                    }
                } else {
                    setError(true);
                }
            } catch (err) {
                setError(true);
            }
        };

        fetchThumbnail();

        return () => {
            active = false;
            if (imgUrl) URL.revokeObjectURL(imgUrl);
        };
    }, [id, token]);

    // Fallback UI (Red Document Icon)
    if (error || !imgUrl) {
        return (
            <div className="w-20 h-28 bg-white shadow-lg rounded-sm border border-gray-100 relative p-2 flex flex-col justify-between overflow-hidden">
                <div className="absolute top-0 right-0 w-6 h-6 bg-red-500" style={{ clipPath: 'polygon(100% 0, 0 0, 100% 100%)' }}></div>
                <div className="space-y-1 mt-2">
                    <div className="h-1.5 w-3/4 bg-gray-100 rounded"></div>
                    <div className="h-1.5 w-1/2 bg-gray-100 rounded"></div>
                </div>
                <div className="text-[10px] font-black text-red-600 text-center tracking-tighter">PDF</div>
            </div>
        );
    }

    return (
        <img
            src={imgUrl}
            alt="Thumbnail"
            className="w-full h-full object-cover object-top opacity-90 group-hover:opacity-100 transition-opacity"
        />
    );
};

// --- Main Page Component ---
export default function SyllabiLibrary() {
    const [syllabi, setSyllabi] = useState<Syllabus[]>([]);
    const [loading, setLoading] = useState(true);
    const [token, setToken] = useState<string>("");
    const [userId, setUserId] = useState<string>("");
    const fileInputRef = useRef<HTMLInputElement>(null);

    // Fetch Helper
    const fetchSyllabi = async (currentToken: string) => {
        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const response = await fetch(`${apiUrl}/syllabi`, {
                headers: currentToken ? { Authorization: `Bearer ${currentToken}` } : {}
            });
            if (response.ok) {
                const data = await response.json();
                setSyllabi(Array.isArray(data) ? data : []);
            }
        } catch (error) {
            console.error("Failed to fetch library:", error);
        } finally {
            setLoading(false);
        }
    };

    // Initial Load
    useEffect(() => {
        const storedToken = localStorage.getItem("token") || localStorage.getItem("access_token") || "";
        const storedUserId = localStorage.getItem("user_id") || "";
        setToken(storedToken);
        setUserId(storedUserId);
        fetchSyllabi(storedToken);
    }, []);

    const handleDelete = async (id: number) => {
        if (!confirm("Are you sure you want to delete this syllabus? This cannot be undone.")) {
            return;
        }
        const token = localStorage.getItem("token");
        try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const res = await fetch(`${apiUrl}/syllabi/${id}`, {
                method: "DELETE",
                headers: { Authorization: `Bearer ${token}` },
            });
            if (res.ok) {
                setSyllabi(prev => prev.filter(s => s.id !== id));
            } else {
                alert("Failed to delete syllabus");
            }
        } catch (err) {
            alert("Server error");
        }
    };

    // Handle File Selection (Upload)
    const handleFileChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
        const file = event.target.files?.[0];
        if (!file || !userId || !token) return;

        const formData = new FormData();
        formData.append("file", file);

        try {
            // 1. Upload the file
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
            const response = await fetch(`${apiUrl}/upload/${userId}`, {
                method: "POST",
                headers: { Authorization: `Bearer ${token}` },
                body: formData,
            });

            if (response.ok) {
                // 2. Refresh immediately so the card shows up
                await fetchSyllabi(token);
            } else {
                alert("Failed to upload syllabus.");
            }
        } catch (error) {
            console.error("Upload error:", error);
        }
    };

    const onUploadClick = () => {
        fileInputRef.current?.click();
    };

    const openPdf = (id: number) => {
        const apiUrl = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
        window.open(`${apiUrl}/view-pdf/${id}`, "_blank");
    };

    const formatDate = (dateStr: string) => {
        return new Date(dateStr).toLocaleDateString("en-US", {
            month: "short", day: "numeric", year: "numeric",
        });
    };

    if (loading) {
        return (
            <div className="flex items-center justify-center min-h-[50vh]">
                <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
            </div>
        );
    }

    return (
        <div className="max-w-7xl mx-auto p-8 font-sans pb-24 relative min-h-screen">
            <div className="mb-8">
                <h1 className="text-3xl font-bold text-gray-900">Syllabi Library</h1>
                <p className="text-gray-500 mt-2">View and manage your uploaded course documents.</p>
            </div>

            {/* Hidden File Input */}
            <input
                type="file"
                ref={fileInputRef}
                onChange={handleFileChange}
                className="hidden"
                accept="application/pdf"
            />

            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-5 gap-8">
                {/* Upload New Card */}
                <div
                    onClick={onUploadClick}
                    className="group bg-gray-50 border-2 border-dashed border-gray-300 rounded-xl flex flex-col items-center justify-center h-80 hover:border-blue-400 hover:bg-blue-50 transition-all cursor-pointer"
                >
                    <div className="w-12 h-12 rounded-full bg-white border border-gray-200 flex items-center justify-center group-hover:scale-110 transition-transform">
                        <span className="text-2xl text-gray-400 group-hover:text-blue-500">+</span>
                    </div>
                    <p className="mt-4 text-sm font-semibold text-gray-500 group-hover:text-blue-600">
                        Upload New
                    </p>
                </div>

                {/* Syllabus Cards */}
                {syllabi.map((s) => (
                    <div
                        key={s.id}
                        onClick={() => openPdf(s.id)}
                        className="group bg-white border border-gray-200 rounded-xl shadow-sm flex flex-col h-80 hover:shadow-2xl hover:-translate-y-1 transition-all cursor-pointer overflow-hidden"
                    >
                        {/* Thumbnail Area */}
                        <div className="h-48 bg-gray-100 flex items-center justify-center relative overflow-hidden">
                            <PDFThumbnail id={s.id} token={token} />
                        </div>

                        {/* Content Area */}
                        <div className="p-4 flex-1 flex flex-col justify-between">
                            <div>
                                <h3 className="text-xs font-bold text-gray-800 uppercase line-clamp-2 leading-tight mb-1">
                                    {s.course_name}
                                </h3>

                                <div className="flex items-center justify-between mt-2">
                                    {/* Link to Calendar Page (Only if parsed) */}
                                    {s.parser_output ? (
                                        <Link
                                            href={`/dashboard/calendar/${s.id}`}
                                            onClick={(e) => e.stopPropagation()}
                                            className="text-[10px] font-bold text-blue-600 hover:text-blue-800 bg-blue-50 hover:bg-blue-100 px-2 py-1 rounded transition-colors flex items-center gap-1"
                                        >
                                            View Schedule &rarr;
                                        </Link>
                                    ) : (
                                        <span className="text-[10px] text-gray-400 font-medium">Processing...</span>
                                    )}

                                    {/* Delete Button */}
                                    <button
                                        onClick={(e) => {
                                            e.stopPropagation();
                                            handleDelete(s.id);
                                        }}
                                        className="text-red-500 hover:text-red-700 hover:bg-red-50 p-2 rounded-full transition-colors"
                                        title="Delete Syllabus"
                                    >
                                        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" className="w-5 h-5">
                                            <path strokeLinecap="round" strokeLinejoin="round" d="m14.74 9-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 0 1-2.244 2.077H8.084a2.25 2.25 0 0 1-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 0 0-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 0 1 3.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 0 0-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 0 0-7.5 0" />
                                        </svg>
                                    </button>
                                </div>
                            </div>

                            <div className="pt-2 border-t border-gray-50 mt-2">
                                <span className="text-[9px] text-gray-400 font-medium">
                                    ADDED {formatDate(s.upload_date)}
                                </span>
                            </div>
                        </div>
                    </div>
                ))}
            </div>

            {/* --- DISCLAIMER FOOTER --- */}
            <div className="fixed bottom-0 left-0 right-0 bg-blue-50 border-t border-blue-100 p-3 text-center z-50">
                <p className="text-xs text-blue-700 font-medium">
                    <span className="font-bold">Note:</span> Large PDF files may take a few minutes to process.
                    Events will appear in the calendar automatically once parsing is complete.
                </p>
            </div>
        </div>
    );
}